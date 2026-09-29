package provider

import (
	"context"
	"time"

	"github.com/spk/spk-ocular/internal/core"
)

// LogSource is implemented by sessions that can stream logs.
type LogSource interface {
	// LogInfo: the channels of ref (containers) and what can be asked.
	LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error)
	// StreamLogs writes ref's logs to sink until ctx ends or, without
	// Follow, the logs are complete. Per-source problems are sink states,
	// not errors; an error means the whole stream failed.
	StreamLogs(ctx context.Context, ref core.Ref, q LogQuery, sink LogSink) error
}

// ChannelAll selects every channel (all containers).
const ChannelAll = "*"

// TailAll asks for all retained lines (within the provider's byte budget).
const TailAll = -1

type LogQuery struct {
	// Channel: "" = the default one, ChannelAll = all.
	Channel  string `json:"channel,omitempty"`
	Previous bool   `json:"previous,omitempty"`
	Follow   bool   `json:"follow,omitempty"`
	// TailLines: N > 0 last lines, TailAll = all; 0 is invalid (the UI
	// never asks for "no history").
	TailLines int `json:"tailLines"`
	// SinceTime: an absolute cutoff (the UI's relative "since" is fixed
	// once at open, so retries and new sources use the same window).
	SinceTime time.Time `json:"sinceTime,omitzero"`
}

// Line flags.
const (
	// LineCut: the line was longer than the provider's limit; the text is
	// its beginning.
	LineCut = 1 << iota
	// LineNoTime: the line had no timestamp (it does not move the cursor).
	LineNoTime
)

// LogLine is one logical line. TS is the provider's timestamp as text
// (RFC3339Nano for k8s: nanoseconds survive to the UI); "" when unknown.
type LogLine struct {
	TS    string
	Text  string
	Flags int
}

// Log states (LogState.State) of a source or (id 0) the whole stream.
const (
	LogStreaming = "streaming"
	LogWaiting   = "waiting"   // e.g. container not started / restarting
	LogEnded     = "ended"     // the source is finished (pod gone, container done)
	LogError     = "error"     // Class says why; the source may retry
	LogGap       = "gap"       // lines may be missing or repeated around here
	LogTruncated = "truncated" // older lines were not fetched (a byte budget)
	LogLimited   = "limited"   // not every source is shown (Message: how many)
)

type LogState struct {
	State   string     `json:"state"`
	Class   ErrorClass `json:"class,omitempty"`
	Message string     `json:"message,omitempty"`
}

// LogSink receives a stream. Calls may block (backpressure from a slow
// page) and fail when the page went away; the provider then stops.
type LogSink interface {
	// Source announces a source before its first lines or states. key is
	// stable for the same source (k8s: pod UID + container).
	Source(id int, key, label, channel string) error
	Lines(id int, lines []LogLine) error
	State(id int, st LogState) error
	// Ready: the initial backlog was delivered; later lines are live.
	Ready() error
}
