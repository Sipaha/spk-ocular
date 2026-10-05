package agentapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/privatefs"
	"github.com/spk/spk-ocular/internal/provider"
)

type ExportLogsRequest struct {
	Ref        core.Ref     `json:"ref" jsonschema:"required"`
	Channel    string       `json:"channel,omitempty" jsonschema_description:"Default channel when omitted; * for all; see GetLogInfo."`
	Previous   bool         `json:"previous,omitempty"`
	SinceTime  time.Time    `json:"sinceTime,omitzero"`
	UntilTime  time.Time    `json:"untilTime,omitzero"`
	Grep       *api.LogGrep `json:"grep,omitempty"`
	Limit      int          `json:"limit,omitempty" jsonschema_description:"Maximum matching lines written: 1..10000000. Required with either time bound; otherwise defaults to 1000000."`
	MaxBytes   int64        `json:"maxBytes,omitempty" jsonschema_description:"Maximum file size: 4096..1073741824 bytes; default 67108864 (64 MiB)."`
	MaxSeconds int          `json:"maxSeconds,omitempty" jsonschema_description:"Maximum export duration: 1..300 seconds; default 120."`
	Format     string       `json:"format,omitempty" jsonschema_description:"text (default, .log) or ndjson (.jsonl). The API returns metadata only, never the log contents."`
}

type ExportLogsView struct {
	Path       string   `json:"path"`
	Format     string   `json:"format"`
	Lines      int      `json:"lines"`
	Bytes      int64    `json:"bytes"`
	Sources    int      `json:"sources"`
	Complete   bool     `json:"complete"`
	Truncated  bool     `json:"truncated"`
	StopReason string   `json:"stopReason"`
	Warnings   []string `json:"warnings,omitempty"`
}

func (s *Server) exportLogs(ctx context.Context, c caller, req *ExportLogsRequest) (*ExportLogsView, error) {
	query := api.TailRequest{Ref: req.Ref, Channel: req.Channel, Previous: req.Previous, SinceTime: req.SinceTime, UntilTime: req.UntilTime, Grep: req.Grep, TailLines: provider.TailAll}
	if err := api.ValidateAgentLogSelection(query); err != nil {
		return nil, err
	}
	if req.Limit < 0 || req.Limit > 10_000_000 || req.Limit == 0 && (!req.SinceTime.IsZero() || !req.UntilTime.IsZero()) {
		return nil, badRequest("limit must be 1..10000000 and is required for a time interval")
	}
	if req.MaxBytes != 0 && (req.MaxBytes < 4096 || req.MaxBytes > 1<<30) {
		return nil, badRequest("maxBytes must be 4096..1073741824")
	}
	if req.MaxSeconds < 0 || req.MaxSeconds > 300 {
		return nil, badRequest("maxSeconds must be 1..300 (default 120)")
	}
	format := req.Format
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "ndjson" {
		return nil, badRequest("format must be text or ndjson")
	}
	if s.o.Downloads == nil {
		return nil, &api.CodedError{Code: api.CodeUnsupported, Detail: "log export destination is not configured"}
	}
	ctx, done, err := s.beginLogRead(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	defer done()
	seconds, limit, maxBytes := req.MaxSeconds, req.Limit, req.MaxBytes
	if seconds == 0 {
		seconds = 120
	}
	if limit == 0 {
		limit = 1_000_000
	}
	if maxBytes == 0 {
		maxBytes = 64 << 20
	}
	exportCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	x, err := s.openLogs(exportCtx, c, "ExportLogs", req.Ref)
	if err != nil {
		return nil, err
	}
	defer x.done()
	dir, err := s.o.Downloads()
	if err != nil {
		return nil, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ext := ".log"
	if format == "ndjson" {
		ext = ".jsonl"
	}
	file, err := privatefs.CreateTemp(dir, "spk-ocular-logs-"+time.Now().UTC().Format("20060102T150405Z")+"-*"+ext)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(file.Name())
		}
	}()
	sink := &exportLogSink{ctx: x.call.Context(), cancel: cancel, writer: bufio.NewWriterSize(file, 64<<10), format: format, limit: limit, maxBytes: maxBytes, sources: map[int]api.TailSource{}}
	err = x.call.ExportAgentLogs(query, sink)
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if sink.failed != nil {
		return nil, sink.failed
	}
	if errors.Is(exportCtx.Err(), context.DeadlineExceeded) && sink.reason == "" {
		sink.reason = "time_limit"
	}
	if err != nil && sink.reason == "" {
		if sink.lines == 0 {
			return nil, err
		}
		sink.reason = "source_error"
		sink.warn(detailOf(err))
	}
	if err := sink.writer.Flush(); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if sink.reason == "" {
		sink.reason = "end"
	}
	truncated := sink.truncated || sink.reason != "end"
	out := &ExportLogsView{Path: file.Name(), Format: format, Lines: sink.lines, Bytes: sink.bytes, Sources: len(sink.sources), Complete: !truncated, Truncated: truncated, StopReason: sink.reason, Warnings: sink.warnings}
	s.read(c, "ExportLogs", req.Ref.Provider, req.Ref.Target, req.Ref.Scope, objectOf(req.Ref))
	keep = true
	return out, nil
}

type exportLogSink struct {
	ctx       context.Context
	cancel    context.CancelFunc
	writer    *bufio.Writer
	format    string
	limit     int
	maxBytes  int64
	mu        sync.Mutex
	lines     int
	bytes     int64
	sources   map[int]api.TailSource
	truncated bool
	reason    string
	warnings  []string
	failed    error
}

func (s *exportLogSink) warn(message string) {
	if len(s.warnings) >= 16 {
		return
	}
	if len(message) > 512 {
		message = message[:512] + "…"
	}
	for _, old := range s.warnings {
		if old == message {
			return
		}
	}
	s.warnings = append(s.warnings, message)
}

func (s *exportLogSink) Source(id int, _, label, channel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sources) >= 1000 {
		s.reason = "source_limit"
		s.cancel()
		return errLogBudget
	}
	s.sources[id] = api.TailSource{ID: id, Label: label, Channel: channel}
	return s.ctx.Err()
}

func (s *exportLogSink) Lines(id int, lines []provider.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range lines {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		if s.lines >= s.limit {
			s.reason = "line_limit"
			s.cancel()
			return errLogBudget
		}
		source := s.sources[id]
		var data []byte
		if s.format == "ndjson" {
			var err error
			data, err = json.Marshal(struct {
				Source api.TailSource `json:"source"`
				TS     string         `json:"ts,omitempty"`
				Text   string         `json:"text"`
				Cut    bool           `json:"cut,omitempty"`
			}{Source: source, TS: line.TS, Text: line.Text, Cut: line.Flags&provider.LineCut != 0})
			if err != nil {
				return err
			}
			data = append(data, '\n')
		} else {
			data = []byte(fmt.Sprintf("%s [%s] %s\n", line.TS, source.Label, line.Text))
		}
		if s.bytes+int64(len(data)) > s.maxBytes {
			s.reason = "byte_limit"
			s.cancel()
			return errLogBudget
		}
		n, err := s.writer.Write(data)
		s.bytes += int64(n)
		if err != nil {
			s.failed = err
			s.cancel()
			return err
		}
		s.lines++
		if line.Flags&provider.LineCut != 0 {
			s.truncated = true
			s.warn("A source truncated an oversized log line.")
		}
	}
	return nil
}

func (s *exportLogSink) State(_ int, state provider.LogState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.State == provider.LogGap || state.State == provider.LogError || state.State == provider.LogTruncated || state.State == provider.LogLimited {
		s.truncated = true
		s.warn(state.Message)
	}
	return s.ctx.Err()
}

func (s *exportLogSink) Ready() error { return s.ctx.Err() }
