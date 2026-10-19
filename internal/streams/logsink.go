package streams

import (
	"github.com/spk/spk-ocular/internal/provider"
)

// Log stream frames (NDJSON, "k" = kind). The UI half is web/src/logs/ndjson.ts.
type (
	sourceFrame struct {
		K       string `json:"k"` // "source"
		ID      int    `json:"id"`
		Key     string `json:"key"`
		Label   string `json:"label"`
		Channel string `json:"channel,omitempty"`
	}
	// linesFrame.L: [ts, text] or [ts, text, flags] per line.
	linesFrame struct {
		K string  `json:"k"` // "lines"
		S int     `json:"s"`
		L [][]any `json:"l"`
	}
	stateFrame struct {
		K       string `json:"k"` // "state"
		S       int    `json:"s"`
		State   string `json:"state"`
		Class   string `json:"class,omitempty"`
		Message string `json:"msg,omitempty"`
	}
	readyFrame struct {
		K string `json:"k"` // "ready"
	}
)

// LogSink adapts a stream Writer to provider.LogSink.
type LogSink struct{ W *Writer }

var _ provider.LogSink = LogSink{}

func (s LogSink) Source(id int, key, label, channel string) error {
	return s.W.Frame(sourceFrame{K: "source", ID: id, Key: key, Label: label, Channel: channel})
}

func (s LogSink) Lines(id int, lines []provider.LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	l := make([][]any, len(lines))
	for i, ln := range lines {
		if ln.Flags != 0 {
			l[i] = []any{ln.TS, ln.Text, ln.Flags}
		} else {
			l[i] = []any{ln.TS, ln.Text}
		}
	}
	return s.W.Frame(linesFrame{K: "lines", S: id, L: l})
}

func (s LogSink) State(id int, st provider.LogState) error {
	return s.W.Frame(stateFrame{K: "state", S: id, State: st.State, Class: string(st.Class), Message: st.Message})
}

func (s LogSink) Ready() error {
	if err := s.W.Frame(readyFrame{K: "ready"}); err != nil {
		return err
	}
	return s.W.Flush() // the backlog is complete: show it now
}

// Preview replaces provisional history atomically in the reader. Large final
// snapshots still travel as the same bounded line frames as live output.
func (s LogSink) Preview(lines []provider.LogPreviewLine, _ bool) error {
	if err := s.W.Frame(readyFrame{K: "history-start"}); err != nil {
		return err
	}
	for i := 0; i < len(lines); {
		id := lines[i].Source
		var batch []provider.LogLine
		size := 0
		for i < len(lines) && lines[i].Source == id && len(batch) < 500 && size < 64<<10 {
			batch = append(batch, lines[i].Line)
			size += len(lines[i].Line.Text) + len(lines[i].Line.TS)
			i++
		}
		if err := s.Lines(id, batch); err != nil {
			return err
		}
	}
	if err := s.W.Frame(readyFrame{K: "history-end"}); err != nil {
		return err
	}
	return s.W.Flush()
}
