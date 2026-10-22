package agentapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const maxLogReads = 4

type activeLogRead struct {
	ref    core.Ref
	cancel context.CancelCauseFunc
}

// Register before reading grants: a concurrent save cannot fall between the
// permission check and registration and leave a revoked stream alive.
func (s *Server) beginLogRead(ctx context.Context, ref core.Ref) (context.Context, func(), error) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if len(s.logReads) >= maxLogReads {
		return nil, nil, &api.CodedError{Code: api.CodeLimit, Detail: "At most four agent log reads may run at once; close another reader first."}
	}
	ctx, cancel := context.WithCancelCause(ctx)
	read := &activeLogRead{ref: ref, cancel: cancel}
	s.logReads[read] = struct{}{}
	stop := context.AfterFunc(s.reqCtx, func() { cancel(context.Canceled) })
	return ctx, func() {
		stop()
		cancel(context.Canceled)
		s.logMu.Lock()
		delete(s.logReads, read)
		s.logMu.Unlock()
	}, nil
}

func (s *Server) cancelLogReads(providerID, target string) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	for read := range s.logReads {
		if providerID == "" || read.ref.Provider == providerID && read.ref.Target == target {
			read.cancel(forbidden("log access changed or was revoked; check Access before reopening logs"))
		}
	}
}

func (s *Server) openLogs(ctx context.Context, c caller, method string, ref core.Ref) (*session, error) {
	if ref.Name == "" {
		return nil, badRequest("ref.name is required")
	}
	x, err := s.open(ctx, c, method, ref.Provider, ref.Target)
	if err != nil {
		return nil, err
	}
	k, err := x.kind(ref.Kind)
	if err == nil && !k.Logs {
		err = &api.CodedError{Code: api.CodeUnsupported, Detail: fmt.Sprintf("%s have no logs", k.ID)}
	}
	if err == nil {
		_, err = s.check(c, x, method, k, ref, agentgrant.VerbLogs, false)
	}
	if err != nil {
		x.done()
		return nil, err
	}
	return x, nil
}

type GetLogInfoRequest struct {
	Ref core.Ref `json:"ref" jsonschema:"required"`
}

func (s *Server) getLogInfo(ctx context.Context, c caller, req *GetLogInfoRequest) (*core.LogInfo, error) {
	ctx, done, err := s.beginLogRead(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	defer done()
	x, err := s.openLogs(ctx, c, "GetLogInfo", req.Ref)
	if err != nil {
		return nil, err
	}
	defer x.done()
	out, err := x.call.AgentLogInfo(req.Ref)
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if err != nil {
		return nil, err
	}
	s.read(c, "GetLogInfo", req.Ref.Provider, req.Ref.Target, req.Ref.Scope, objectOf(req.Ref))
	return &out, nil
}

func (s *Server) getLogs(ctx context.Context, c caller, req *GetLogsRequest) (*api.Tail, error) {
	query := req.query()
	if err := api.ValidateAgentLogs(query); err != nil {
		return nil, err
	}
	ctx, done, err := s.beginLogRead(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	defer done()
	x, err := s.openLogs(ctx, c, "GetLogs", req.Ref)
	if err != nil {
		return nil, err
	}
	defer x.done()
	out, err := x.call.TailLogs(query)
	if ctx.Err() != nil {
		return nil, context.Cause(ctx)
	}
	if err != nil {
		return nil, err
	}
	s.read(c, "GetLogs", req.Ref.Provider, req.Ref.Target, req.Ref.Scope, objectOf(req.Ref))
	_, bytes := api.AgentLogLimits(query)
	out = boundLogJSON(out, bytes)
	return &out, nil
}

type StreamLogsRequest struct {
	GetLogsRequest
	Follow     bool `json:"follow,omitempty" jsonschema_description:"Keep reading new lines after retained history. Stops on disconnect, access/configuration change, shutdown or untilTime. Previous-container logs cannot be followed."`
	MaxSeconds int  `json:"maxSeconds,omitempty" jsonschema_description:"Maximum read duration: 1..60 seconds, default 20, including follow mode."`
}

// LogFrame preserves the bounded source and completion semantics across MCP framing.
type LogFrame struct {
	Type       string             `json:"type" jsonschema_description:"start, source, lines, state, ready, ping, error, end. A response without an end frame is incomplete."`
	Source     int                `json:"source,omitempty"`
	Key        string             `json:"key,omitempty"`
	Label      string             `json:"label,omitempty"`
	Channel    string             `json:"channel,omitempty"`
	Lines      []api.TailLine     `json:"lines,omitempty"`
	State      *provider.LogState `json:"state,omitempty"`
	Error      *api.CodedError    `json:"error,omitempty"`
	Complete   bool               `json:"complete,omitempty" jsonschema_description:"End only: true when history or the requested follow interval finished without an error; inspect truncated and source states as well."`
	Truncated  bool               `json:"truncated,omitempty"`
	StopReason string             `json:"stopReason,omitempty" jsonschema_description:"End only: end, line_limit, byte_limit, time_limit, or error. A reached budget means more matching data may exist."`
}

// LogStreamResult is one finite capture. Progress messages are optional;
// these bounded frames, including the final end state, are authoritative.
type LogStreamResult struct {
	Frames []LogFrame `json:"frames"`
}

func (s *Server) registerLogStream() {
	register(s, "StreamLogs", agentgrant.VerbLogs, false,
		"Capture finite log frames with the same logs grant as GetLogs. limit is required (1..5000); maxBytes defaults to 65536 (maximum 1048576); maxSeconds defaults to 20 (maximum 60), including follow. With a progress token, progress messages carry JSON frames during capture; the final structured frames are authoritative. Inspect source states and the final end frame. Cancellation ends the read. This never follows indefinitely.", s.collectLogs)
	s.methods[len(s.methods)-1].Streaming = true
}

// logOutput keeps the capture independent of HTTP or MCP framing.
type logOutput interface {
	io.Writer
	Flush() error
	SetWriteDeadline(time.Time) error
	Start()
}

type collectedLogs struct {
	frames []LogFrame
	emit   func(LogFrame) error
}

func (c *collectedLogs) Start()                           {}
func (c *collectedLogs) Flush() error                     { return nil }
func (c *collectedLogs) SetWriteDeadline(time.Time) error { return nil }
func (c *collectedLogs) Write(data []byte) (int, error) {
	var frame LogFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return 0, err
	}
	if c.emit != nil {
		if err := c.emit(frame); err != nil {
			return 0, err
		}
	}
	c.frames = append(c.frames, frame)
	return len(data), nil
}

type logProgressKey struct{}

func (s *Server) collectLogs(ctx context.Context, c caller, req *StreamLogsRequest) (*LogStreamResult, error) {
	output := &collectedLogs{frames: []LogFrame{}}
	output.emit, _ = ctx.Value(logProgressKey{}).(func(LogFrame) error)
	if err := s.streamLogs(ctx, c, req, output); err != nil {
		return nil, err
	}
	// A finite MCP result must not release newly buffered payload after access
	// was revoked. Earlier progress cannot be retracted, as with a live stream.
	for _, frame := range output.frames {
		if frame.Error != nil && (frame.Error.Code == "forbidden" || frame.Error.Code == "gone" || frame.Error.Code == "unauthorized") {
			return nil, frame.Error
		}
	}
	return &LogStreamResult{Frames: output.frames}, nil
}

func (s *Server) streamLogs(ctx context.Context, c caller, req *StreamLogsRequest, output logOutput) error {
	query := req.query()
	if err := api.ValidateAgentLogs(query); err != nil {
		return err
	}
	if req.Previous && req.Follow || req.Limit < 1 || req.MaxSeconds < 0 || req.MaxSeconds > 60 {
		return badRequest("StreamLogs requires limit (1..5000); maxSeconds is 1..60 (default 20); previous logs cannot be followed")
	}
	ctx, done, err := s.beginLogRead(ctx, req.Ref)
	if err != nil {
		return err
	}
	defer done()
	seconds := req.MaxSeconds
	if seconds == 0 {
		seconds = 20
	}
	streamCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	x, err := s.openLogs(streamCtx, c, "StreamLogs", req.Ref)
	if err != nil {
		return err
	}
	defer x.done()
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	s.read(c, "StreamLogs", req.Ref.Provider, req.Ref.Target, req.Ref.Scope, objectOf(req.Ref))
	output.Start()
	limit, bytes := api.AgentLogLimits(query)
	sink := &agentLogWriter{ctx: x.call.Context(), cancel: cancel, writer: output, control: output, limit: limit, maxBytes: bytes}
	defer func() { _ = sink.control.SetWriteDeadline(time.Time{}) }()
	deadlineDone := make(chan struct{})
	stopDeadline := context.AfterFunc(x.call.Context(), func() {
		_ = sink.control.SetWriteDeadline(time.Now())
		close(deadlineDone)
	})
	var deadlineOnce sync.Once
	stopAndWaitDeadline := func() {
		deadlineOnce.Do(func() {
			if !stopDeadline() {
				<-deadlineDone
			}
		})
	}
	defer stopAndWaitDeadline()
	if err := sink.frame(LogFrame{Type: "start"}); err != nil {
		return err
	}
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-sink.ctx.Done():
				return
			case <-tick.C:
				if sink.frame(LogFrame{Type: "ping"}) != nil {
					return
				}
			}
		}
	}()
	err = x.call.StreamAgentLogs(query, req.Follow, sink)
	if errors.Is(streamCtx.Err(), context.DeadlineExceeded) {
		sink.mu.Lock()
		if sink.reason == "" {
			sink.reason = "time_limit"
		}
		sink.mu.Unlock()
	}
	// The cancellation wake-up must finish before the final frames and
	// net/http's chunk terminator, or its expired deadline can cut them off.
	stopAndWaitDeadline()
	cancel()
	<-heartbeatDone
	if ctx.Err() != nil {
		err = context.Cause(ctx)
	}
	sink.finish(err)
	return nil
}

type agentLogWriter struct {
	ctx             context.Context
	cancel          context.CancelFunc
	writer          io.Writer
	control         logOutput
	mu              sync.Mutex
	failed          bool
	truncated       bool
	limit, maxBytes int
	lines, bytes    int
	reason          string
}

var errLogBudget = errors.New("log output budget reached")

const logCompletionReserve = 2048

func (s *agentLogWriter) write(frame LogFrame) error {
	_ = s.control.SetWriteDeadline(time.Now().Add(2 * time.Second))
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if frame.Type != "error" && frame.Type != "end" && s.bytes+len(data) > s.maxBytes-logCompletionReserve {
		s.reason = "byte_limit"
		s.cancel()
		return errLogBudget
	}
	_, err = s.writer.Write(data)
	s.bytes += len(data)
	if err == nil {
		err = s.control.Flush()
	}
	if err != nil {
		s.failed = true
		s.cancel()
	}
	return err
}

func (s *agentLogWriter) frame(frame LogFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	return s.write(frame)
}

func (s *agentLogWriter) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed {
		return
	}
	if s.reason != "" {
		_ = s.write(LogFrame{Type: "end", StopReason: s.reason, Truncated: true})
		return
	}
	if err != nil {
		ce := *errCoded(err)
		if len(ce.Detail) > 256 {
			ce.Detail = ce.Detail[:256] + "…"
		}
		if s.write(LogFrame{Type: "error", Error: &ce}) != nil {
			return
		}
	}
	reason := "end"
	if err != nil {
		reason = "error"
	}
	_ = s.write(LogFrame{Type: "end", Complete: err == nil, Truncated: s.truncated || err != nil, StopReason: reason})
}

func (s *agentLogWriter) Source(id int, key, label, channel string) error {
	return s.frame(LogFrame{Type: "source", Source: id, Key: key, Label: label, Channel: channel})
}

func (s *agentLogWriter) Lines(id int, lines []provider.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Bound each JSON frame independently of the provider's batch size.
	for len(lines) > 0 {
		if s.ctx.Err() != nil {
			return s.ctx.Err()
		}
		if s.lines >= s.limit {
			s.reason = "line_limit"
			s.cancel()
			return errLogBudget
		}
		n, size := 0, 0
		for n < len(lines) && n < 128 && n < s.limit-s.lines && (n == 0 || size+len(lines[n].Text) <= 64<<10) {
			size += len(lines[n].Text)
			n++
		}
		out := make([]api.TailLine, n)
		for i, line := range lines[:n] {
			out[i] = api.TailLine{Source: id, TS: line.TS, Text: line.Text, Cut: line.Flags&provider.LineCut != 0}
		}
		// Keep the largest prefix that fits, including JSON overhead.
		lo, hi := 1, len(out)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			data, _ := json.Marshal(LogFrame{Type: "lines", Source: id, Lines: out[:mid]})
			if s.bytes+len(data)+1 <= s.maxBytes-logCompletionReserve {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		out, n = out[:lo], lo
		if err := s.write(LogFrame{Type: "lines", Source: id, Lines: out}); err != nil {
			return err
		}
		s.lines += n
		if s.lines >= s.limit {
			s.reason = "line_limit"
			s.cancel()
			return errLogBudget
		}
		lines = lines[n:]
	}
	return nil
}

func (s *agentLogWriter) State(id int, state provider.LogState) error {
	s.mu.Lock()
	if state.State == provider.LogTruncated || state.State == provider.LogLimited || state.State == provider.LogGap || state.State == provider.LogError {
		s.truncated = true
	}
	s.mu.Unlock()
	return s.frame(LogFrame{Type: "state", Source: id, State: &state})
}

func (s *agentLogWriter) Ready() error { return s.frame(LogFrame{Type: "ready"}) }
