package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// LogGrep filters the log text before the result's line and byte budgets.
type LogGrep struct {
	Pattern    string `json:"pattern" jsonschema:"required" jsonschema_description:"Nonempty text or regular expression, at most 4096 bytes; matched against the log text, not its timestamp or source label."`
	Regex      bool   `json:"regex,omitempty" jsonschema_description:"Interpret pattern as a Go/RE2 regular expression; otherwise it is literal text."`
	IgnoreCase bool   `json:"ignoreCase,omitempty"`
	Invert     bool   `json:"invert,omitempty" jsonschema_description:"Select lines that do not match."`
}

func (g *LogGrep) compile() (*regexp.Regexp, error) {
	if g == nil {
		return nil, nil
	}
	if g.Pattern == "" || len(g.Pattern) > 4096 {
		return nil, coded(CodeBadRequest, errors.New("grep.pattern must contain 1..4096 bytes"))
	}
	pattern := g.Pattern
	if !g.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if g.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, coded(CodeBadRequest, fmt.Errorf("invalid grep pattern: %w", err))
	}
	return re, nil
}

// AgentLogLimits returns the effective, bounded output budgets.
func AgentLogLimits(req TailRequest) (int, int) {
	lines, bytes := req.Limit, req.MaxBytes
	if lines == 0 {
		lines = min(200, maxTailLines)
	}
	if bytes == 0 {
		bytes = min(64<<10, maxTailBytes)
	}
	return lines, bytes
}

// ValidateAgentLogs checks the request before an HTTP stream is started.
func ValidateAgentLogs(req TailRequest) error {
	if err := ValidateAgentLogSelection(req); err != nil {
		return err
	}
	if req.Limit < 0 || req.Limit > maxTailLines {
		return coded(CodeBadRequest, fmt.Errorf("limit must be 0 (default) or 1..%d", maxTailLines))
	}
	if (!req.SinceTime.IsZero() || !req.UntilTime.IsZero()) && req.Limit == 0 {
		return coded(CodeBadRequest, errors.New("limit is required for a time interval"))
	}
	if req.MaxBytes != 0 && (req.MaxBytes < 4096 || req.MaxBytes > maxTailBytes) {
		return coded(CodeBadRequest, fmt.Errorf("maxBytes must be 4096..%d (default 65536)", maxTailBytes))
	}
	return nil
}

// ValidateAgentLogSelection checks filters independently of output destination.
func ValidateAgentLogSelection(req TailRequest) error {
	if req.TailLines != provider.TailAll && (req.TailLines < 1 || req.TailLines > maxTailLines) {
		return coded(CodeBadRequest, fmt.Errorf("tailLines must be -1 (retained history) or 1..%d", maxTailLines))
	}
	if _, err := req.Grep.compile(); err != nil {
		return err
	}
	if !req.UntilTime.IsZero() && !req.SinceTime.IsZero() && !req.SinceTime.Before(req.UntilTime) {
		return coded(CodeBadRequest, errors.New("sinceTime must be before untilTime"))
	}
	return nil
}

func agentLogQuery(req TailRequest, follow bool) provider.LogQuery {
	n := req.TailLines
	if !req.UntilTime.IsZero() && !follow {
		// Providers without an upper time bound must read retained history;
		// taking today's tail first could hide the entire requested interval.
		n = provider.TailAll
	}
	since := req.SinceTime
	if req.Previous && !since.IsZero() {
		// Older-container APIs do not all support a since parameter. Read
		// retained lines and apply the same time filter locally instead.
		since = time.Time{}
		n = provider.TailAll
	}
	return provider.LogQuery{Channel: req.Channel, Previous: req.Previous, Follow: follow, TailLines: n, SinceTime: since, Archive: !follow && n == provider.TailAll}
}

// AgentLogInfo describes channels without borrowing a UI session or view.
func (c *AgentCall) AgentLogInfo(ref core.Ref) (core.LogInfo, error) {
	src, ok := c.e.sess.(provider.LogSource)
	if !ok {
		return core.LogInfo{}, coded(CodeUnsupported, errors.New("this target has no logs"))
	}
	ctx, cancel := context.WithTimeout(c.ctx, getTimeout)
	defer cancel()
	info, err := src.LogInfo(ctx, ref)
	if err != nil {
		return core.LogInfo{}, c.fail(err)
	}
	return info, nil
}

// StreamAgentLogs keeps only a bounded provider frame in memory. The caller
// owns output deadlines, grant revocation and overall connection limits.
func (c *AgentCall) StreamAgentLogs(req TailRequest, follow bool, sink provider.LogSink) error {
	if err := ValidateAgentLogs(req); err != nil {
		return err
	}
	if req.Previous && follow {
		return coded(CodeBadRequest, errors.New("previous logs cannot be followed"))
	}
	if req.Limit < 1 {
		return coded(CodeBadRequest, errors.New("limit is required for StreamLogs"))
	}
	return c.readAgentLogs(req, follow, sink)
}

// ExportAgentLogs reads retained history into a file sink whose separate
// line/byte/time budgets are enforced by the agent transport.
func (c *AgentCall) ExportAgentLogs(req TailRequest, sink provider.LogSink) error {
	req.TailLines = provider.TailAll
	if err := ValidateAgentLogSelection(req); err != nil {
		return err
	}
	return c.readAgentLogs(req, false, sink)
}

func (c *AgentCall) readAgentLogs(req TailRequest, follow bool, sink provider.LogSink) error {
	src, ok := c.e.sess.(provider.LogSource)
	if !ok {
		return coded(CodeUnsupported, errors.New("this target has no logs"))
	}
	if !req.UntilTime.IsZero() && !req.UntilTime.After(time.Now()) {
		follow = false
	}
	if !follow && req.TailLines > 0 && (!req.UntilTime.IsZero() || req.Previous && !req.SinceTime.IsZero()) {
		// A historical interval's last N lines are known only after reading
		// its end. Use the bounded snapshot path and report any truncation.
		tail, err := c.TailLogs(req)
		if err != nil {
			return err
		}
		for _, source := range tail.Sources {
			if err := sink.Source(source.ID, "", source.Label, source.Channel); err != nil {
				return err
			}
			if source.State != nil {
				if err := sink.State(source.ID, *source.State); err != nil {
					return err
				}
			}
		}
		for _, line := range tail.Lines {
			flags := 0
			if line.Cut {
				flags = provider.LineCut
			}
			if err := sink.Lines(line.Source, []provider.LogLine{{TS: line.TS, Text: line.Text, Flags: flags}}); err != nil {
				return err
			}
		}
		if tail.State != nil {
			if err := sink.State(0, *tail.State); err != nil {
				return err
			}
		}
		if tail.Truncated {
			if err := sink.State(0, provider.LogState{State: provider.LogTruncated, Message: "Earlier lines were omitted or a source reported partial data; use tailLines: -1 to stream the retained interval."}); err != nil {
				return err
			}
		}
		return sink.Ready()
	}
	ctx := c.ctx
	var cancel context.CancelFunc
	if follow && !req.UntilTime.IsZero() {
		ctx, cancel = context.WithDeadline(ctx, req.UntilTime)
		defer cancel()
	}
	err := src.StreamLogs(ctx, req.Ref, agentLogQuery(req, follow), newAgentTimeFilter(sink, req))
	if c.ctx.Err() != nil {
		return c.fail(c.ctx.Err())
	}
	if ctx.Err() == context.DeadlineExceeded && !req.UntilTime.IsZero() {
		return nil
	}
	if err != nil {
		return c.fail(err)
	}
	return nil
}

// Time bounds are [sinceTime, untilTime). Sources can interleave or contain
// out-of-order timestamps: a line beyond untilTime never ends another source.
type agentTimeFilter struct {
	provider.LogSink
	since, until time.Time
}

func newAgentTimeFilter(sink provider.LogSink, req TailRequest) provider.LogSink {
	if req.Grep != nil {
		re, _ := req.Grep.compile() // ValidateAgentLogs already checked it.
		sink = &agentGrepFilter{LogSink: sink, pattern: re, invert: req.Grep.Invert}
	}
	if req.SinceTime.IsZero() && req.UntilTime.IsZero() {
		return sink
	}
	return &agentTimeFilter{LogSink: sink, since: req.SinceTime, until: req.UntilTime}
}

type agentGrepFilter struct {
	provider.LogSink
	pattern *regexp.Regexp
	invert  bool
}

func (f *agentGrepFilter) Lines(id int, lines []provider.LogLine) error {
	selected := make([]provider.LogLine, 0, len(lines))
	for _, line := range lines {
		if f.pattern.MatchString(line.Text) != f.invert {
			selected = append(selected, line)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return f.LogSink.Lines(id, selected)
}

func (f *agentTimeFilter) Lines(id int, lines []provider.LogLine) error {
	selected := make([]provider.LogLine, 0, len(lines))
	unknown := false
	for _, line := range lines {
		ts, err := time.Parse(time.RFC3339Nano, line.TS)
		if err != nil || line.Flags&provider.LineNoTime != 0 {
			unknown = true
			continue
		}
		if !f.since.IsZero() && ts.Before(f.since) || !f.until.IsZero() && !ts.Before(f.until) {
			continue
		}
		selected = append(selected, line)
	}
	if unknown {
		if err := f.State(id, provider.LogState{State: provider.LogGap, Message: "Lines without a valid timestamp were omitted from the requested time interval."}); err != nil {
			return err
		}
	}
	if len(selected) == 0 {
		return nil
	}
	return f.LogSink.Lines(id, selected)
}
