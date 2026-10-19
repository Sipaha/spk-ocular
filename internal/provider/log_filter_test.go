package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type filterRecorder struct {
	lines  []filteredLine
	states []LogState
	ready  bool
	fail   error
}

func (s *filterRecorder) Source(int, string, string, string) error { return nil }
func (s *filterRecorder) State(_ int, st LogState) error {
	s.states = append(s.states, st)
	return s.fail
}
func (s *filterRecorder) Lines(id int, lines []LogLine) error {
	for _, line := range lines {
		s.lines = append(s.lines, filteredLine{id, line})
	}
	return s.fail
}
func (s *filterRecorder) Ready() error { s.ready = true; return s.fail }

func TestLogFilterSemantics(t *testing.T) {
	for _, tc := range []struct {
		pattern, text string
		want          bool
	}{
		{"ERR*OR", "prefix error suffix", true}, {"err*or", "or err", false}, {"[error]", "[ERROR] raw", true},
		{"ERROR", "\x1b[31mERROR\x1b[0m text", true}, {"Привет*МИР", "привет большой мир", true},
		{"*x*", "anything", true}, {"**", "anything", true}, {"no-match", "ERROR", false},
		{"link", "\x1b]8;;https://hidden.example/link\x07ERROR\x1b]8;;\x07", false},
	} {
		require.Equal(t, tc.want, NewLogFilter(tc.pattern).Match(tc.text), tc.pattern+" / "+tc.text)
	}
}
func TestFilteredBacklogCountsMatchesThenStreamsWithoutCountingNoise(t *testing.T) {
	r := &filterRecorder{}
	s := FilterLogSink(LogQuery{TailLines: 2, Filter: "error"}, r)
	for i, text := range []string{"ERROR old", "INFO noise", "ERROR middle", "INFO newer", "ERROR newest", "INFO last"} {
		require.NoError(t, s.Lines(i%2+1, []LogLine{{Text: text}}))
	}
	require.Empty(t, r.lines)
	require.NoError(t, s.Ready())
	require.True(t, r.ready)
	require.Equal(t, "ERROR middle", r.lines[0].line.Text)
	require.Equal(t, "ERROR newest", r.lines[1].line.Text)
	require.NoError(t, s.Lines(1, []LogLine{{Text: "INFO ignored"}, {Text: "ERROR live"}}))
	require.Len(t, r.lines, 3)
	require.Equal(t, "ERROR live", r.lines[2].line.Text)
}
func TestFilteredBacklogByteBudgetIsExplicitAndSinkFailurePropagates(t *testing.T) {
	r := &filterRecorder{}
	s := FilterLogSink(LogQuery{TailLines: TailAll, Filter: "error"}, r)
	for range 80 {
		require.NoError(t, s.Lines(1, []LogLine{{Text: "ERROR " + strings.Repeat("x", 256<<10)}}))
	}
	require.NoError(t, s.Ready())
	require.NotEmpty(t, r.states)
	require.Equal(t, LogTruncated, r.states[0].State)
	require.Less(t, len(r.lines), 80)
	r = &filterRecorder{fail: errors.New("closed")}
	s = FilterLogSink(LogQuery{TailLines: 1, Filter: "error"}, r)
	require.NoError(t, s.Lines(1, []LogLine{{Text: "ERROR"}}))
	require.Error(t, s.Ready())
}
