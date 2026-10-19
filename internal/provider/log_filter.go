package provider

import (
	"regexp"
	"strings"
	"sync"
	"unicode/utf16"
)

// LogFilter implements the UI's case-insensitive substring filter. Asterisks
// match any text; fewer than two non-wildcard UTF-16 units disable filtering.
// Matching is linear, without user-supplied regular expressions.
type LogFilter struct{ parts []string }

func NewLogFilter(pattern string) LogFilter {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if len(utf16.Encode([]rune(strings.ReplaceAll(pattern, "*", "")))) < 2 {
		return LogFilter{}
	}
	return LogFilter{parts: strings.Split(pattern, "*")}
}

func (f LogFilter) Enabled() bool { return len(f.parts) != 0 }

var logEscapes = regexp.MustCompile("\x1b(?:\\[[0-?]*[ -/]*[@-~]|\\][^\x07\x1b]*(?:\x07|\x1b\\\\)|[@-_])")

func (f LogFilter) Match(text string) bool {
	if !f.Enabled() {
		return true
	}
	text = strings.ToLower(logEscapes.ReplaceAllString(text, ""))
	for _, part := range f.parts {
		at := strings.Index(text, part)
		if at < 0 {
			return false
		}
		text = text[at+len(part):]
	}
	return true
}

// FilterLogSink keeps the newest matching backlog lines until Ready, then
// forwards only matching live lines. Providers must read beyond the raw tail
// and apply the same filter before their merged history limit. Raw scan byte
// and time budgets remain independent safety limits and must report truncation.
func FilterLogSink(q LogQuery, sink LogSink) LogSink {
	f := NewLogFilter(q.Filter)
	if !f.Enabled() {
		return sink
	}
	keep := q.TailLines
	if q.LiveOnly {
		keep = 50_000
	}
	if keep < 1 || keep > 50_000 {
		keep = 50_000
	}
	return &filteredLogSink{LogSink: sink, filter: f, keep: keep, liveOnly: q.LiveOnly}
}

type filteredLine struct {
	id   int
	line LogLine
}
type filteredLogSink struct {
	LogSink
	mu           sync.Mutex
	filter       LogFilter
	keep         int
	ready        bool
	liveOnly     bool
	lines        []filteredLine
	start, bytes int
	cut          bool
}

func (s *filteredLogSink) Lines(id int, lines []LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []LogLine
	for _, line := range lines {
		if !s.filter.Match(line.Text) {
			continue
		}
		if s.ready {
			out = append(out, line)
			continue
		}
		s.lines = append(s.lines, filteredLine{id, line})
		s.bytes += len(line.Text) + len(line.TS) + 64
		for len(s.lines)-s.start > s.keep || (s.bytes > 16<<20 && len(s.lines)-s.start > 1) {
			s.cut = s.cut || s.bytes > 16<<20 || s.liveOnly
			old := s.lines[s.start]
			s.bytes -= len(old.line.Text) + len(old.line.TS) + 64
			s.lines[s.start] = filteredLine{}
			s.start++
		}
		if s.start >= s.keep {
			s.lines = append([]filteredLine(nil), s.lines[s.start:]...)
			s.start = 0
		}
	}
	if len(out) > 0 {
		return s.LogSink.Lines(id, out)
	}
	return nil
}

func (s *filteredLogSink) Ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if preview, ok := s.LogSink.(LogPreviewSink); ok {
		rows := make([]LogPreviewLine, 0, len(s.lines)-s.start)
		for _, line := range s.lines[s.start:] {
			rows = append(rows, LogPreviewLine{Source: line.id, Line: line.line})
		}
		if err := preview.Preview(rows, true); err != nil {
			return err
		}
		s.lines = nil
		s.start = 0
		s.bytes = 0
		s.ready = true
		if s.cut {
			if err := s.reportCut(); err != nil {
				return err
			}
		}
		return s.LogSink.Ready()
	}
	for i := s.start; i < len(s.lines); {
		id := s.lines[i].id
		var batch []LogLine
		size := 0
		for i < len(s.lines) && s.lines[i].id == id && len(batch) < 500 && size < 64<<10 {
			line := s.lines[i].line
			batch = append(batch, line)
			size += len(line.Text) + len(line.TS)
			i++
		}
		if err := s.LogSink.Lines(id, batch); err != nil {
			return err
		}
	}
	s.lines = nil
	s.start = 0
	s.bytes = 0
	s.ready = true
	if s.cut {
		if err := s.reportCut(); err != nil {
			return err
		}
	}
	return s.LogSink.Ready()
}

// MatchingLogLines selects text before a provider applies its merged tail cap.
func MatchingLogLines(pattern string, lines []LogLine) []LogLine {
	f := NewLogFilter(pattern)
	if !f.Enabled() {
		return lines
	}
	out := make([]LogLine, 0, len(lines))
	for _, line := range lines {
		if f.Match(line.Text) {
			out = append(out, line)
		}
	}
	return out
}

// FilterScanWindow shares a bounded 10k raw-line read budget across a
// group's initial sources. Upstream log APIs have no offset: each retry replaces
// the prior overlapping tail window instead of appending duplicate records.
// Callers charge overlapping records against the budget too.
func FilterScanWindow(q LogQuery, sources int) (first, ceiling int) {
	ceiling = max(1, 10_000/max(1, sources))
	first = q.TailLines
	if first < 1 {
		first = 500
	}
	return min(first, ceiling), ceiling
}

const FilterScanLimited = "the filter history scan reached its raw-line budget; older matching lines may be missing"

func (s *filteredLogSink) Preview(lines []LogPreviewLine, final bool) error {
	if p, ok := s.LogSink.(LogPreviewSink); ok {
		return p.Preview(lines, final)
	}
	return nil
}

// PreviewLogLines caps provisional UI updates; final history keeps its normal
// larger budget. At most 500 rows / 256 KiB are repainted per preview.
func PreviewLogLines(lines []LogPreviewLine) []LogPreviewLine {
	start, size := len(lines), 0
	for start > 0 && len(lines)-start < 500 {
		line := lines[start-1].Line
		n := len(line.Text) + len(line.TS) + 64
		if size+n > 256<<10 {
			break
		}
		size += n
		start--
	}
	return lines[start:]
}

func (s *filteredLogSink) reportCut() error {
	state, message := LogTruncated, "matching history exceeded the viewer byte budget; older matches were omitted"
	if s.liveOnly {
		state = LogGap
		message = "new matching logs exceeded the bounded live capture buffer; some records were omitted"
	}
	return s.State(0, LogState{State: state, Message: message})
}
