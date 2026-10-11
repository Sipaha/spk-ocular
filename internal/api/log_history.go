package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/streams"
)

const (
	LogHistoryMaxBytes = 1 << 30
	LogHistoryMaxLines = 10_000_000
	logHistoryDuration = 5 * time.Minute
)

type LogHistoryRequest struct {
	Ref      core.Ref `json:"ref"`
	Channel  string   `json:"channel,omitempty"`
	Previous bool     `json:"previous,omitempty"`
}

// OpenLogHistory reads retained history separately from the viewer buffer.
// The finite archive uses normal UI admission and the existing one-shot stream
// transport. It neither follows logs nor expands agent permissions.
func (s *Service) OpenLogHistory(ctx context.Context, req LogHistoryRequest) (LogStreamInfo, error) {
	if len(req.Channel) > 4096 || req.Ref.UID == "" {
		return LogStreamInfo{}, coded(CodeBadRequest, errors.New("log history requires a UID-pinned resource and a valid channel"))
	}
	e, src, err := s.logSource(ctx, req.Ref)
	if err != nil {
		return LogStreamInfo{}, err
	}
	id, err := s.registerStream(e, func(ctx context.Context, out *streams.Writer) error {
		ctx, cancel := context.WithTimeout(ctx, logHistoryDuration)
		defer cancel()
		// Providers validate the requested object UID through their ordinary log
		// metadata read. Refuse an unverifiable or replaced source before reading.
		if _, err := src.LogInfo(ctx, req.Ref); err != nil {
			return logHistoryReadError(err)
		}
		sink := &historyLogSink{ctx: ctx, out: streams.LogSink{W: out}, labels: map[int]string{}, maxBytes: LogHistoryMaxBytes, maxLines: LogHistoryMaxLines}
		err := src.StreamLogs(ctx, req.Ref, provider.LogQuery{Archive: true, Channel: req.Channel, Previous: req.Previous, TailLines: provider.TailAll}, sink)
		if err != nil {
			return logHistoryReadError(err)
		}
		if err = ctx.Err(); err != nil {
			return logHistoryReadError(err)
		}
		// A name-addressed log read cannot carry a Kubernetes UID precondition.
		// Revalidate after it, before the UI can commit a complete destination.
		if _, err = src.LogInfo(ctx, req.Ref); err != nil {
			return logHistoryReadError(err)
		}

		if len(sink.labels) == 0 {
			return incompleteLogHistory()
		}
		return nil
	})
	if err != nil {
		return LogStreamInfo{}, err
	}
	return LogStreamInfo{StreamID: id}, nil
}

type historyLogSink struct {
	ctx      context.Context
	out      provider.LogSink
	mu       sync.Mutex
	labels   map[int]string
	bytes    int64
	lines    int
	maxBytes int64
	maxLines int
}

func (s *historyLogSink) Source(id int, key, label, channel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if id < 1 || len(key) > 4096 || len(label) > 4096 || len(channel) > 4096 {
		return coded(CodeBadRequest, errors.New("invalid log history source"))
	}
	if _, ok := s.labels[id]; !ok && len(s.labels) >= 1000 {
		return limitedLogHistory()
	}
	s.labels[id] = label
	return s.out.Source(id, key, label, channel)
}
func (s *historyLogSink) Lines(id int, lines []provider.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	label, ok := s.labels[id]
	if !ok {
		return coded(CodeBadRequest, errors.New("log history lines arrived without a source"))
	}
	for _, line := range lines {
		if len(line.Text) > 256<<10 || len(line.TS) > 1024 || line.Flags < 0 || line.Flags & ^(provider.LineCut|provider.LineNoTime) != 0 {
			return incompleteLogHistory()
		}
		if line.Flags&provider.LineCut != 0 {
			return incompleteLogHistory()
		}
		size := int64(len(line.TS) + len(label) + len(line.Text) + 5)
		if s.lines >= s.maxLines || size > s.maxBytes-s.bytes {
			return limitedLogHistory()
		}
		s.lines++
		s.bytes += size
	}
	// Keep transport frames bounded even if a provider supplies a larger batch.
	start, size := 0, 0
	for i, line := range lines {
		if i > start && (i-start >= 500 || size+len(line.Text) > 64<<10) {
			if err := s.out.Lines(id, lines[start:i]); err != nil {
				return err
			}
			start = i
			size = 0
		}
		size += len(line.Text)
	}
	if start < len(lines) {
		return s.out.Lines(id, lines[start:])
	}
	return nil
}
func (s *historyLogSink) State(id int, state provider.LogState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := s.out.State(id, state); err != nil {
		return err
	}
	switch state.State {
	case provider.LogGap, provider.LogError, provider.LogTruncated, provider.LogLimited, provider.LogWaiting:
		return incompleteLogHistory()
	case provider.LogEnded:
		if state.Class != "" || state.Message != "" {
			return incompleteLogHistory()
		}
		return nil
	case provider.LogStreaming:
		return nil
	default:
		return coded(CodeBadRequest, errors.New("invalid log history source state"))
	}
}
func (s *historyLogSink) Ready() error { return s.ctx.Err() }

func incompleteLogHistory() error {
	const text = "Complete retained history could not be downloaded; the destination was not replaced."
	return &CodedError{Code: CodeInternal, Detail: text, Why: &core.Message{Key: "logs.historyIncomplete", Text: text}}
}
func limitedLogHistory() error {
	const text = "History export limit reached: 1 GiB, 10000000 lines, 1000 sources or 5 minutes."
	return &CodedError{Code: CodeLimit, Detail: text, Why: &core.Message{Key: "logs.historyLimit", Text: text}}
}

func logHistoryReadError(err error) error {
	if errors.Is(err, context.Canceled) {
		return incompleteLogHistory()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return limitedLogHistory()
	}
	return fromProvider(err)
}
