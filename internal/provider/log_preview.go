package provider

import (
	"sort"
	"sync"
	"time"
)

// LogPreview coalesces partial source histories without rescanning the raw
// backlog per line. It owns one timer per group and no unbounded update queue.
type LogPreview struct {
	mu         sync.Mutex
	sink       LogPreviewSink
	filter     LogFilter
	rows       map[int][]LogLine
	timer      *time.Timer
	last       time.Time
	closed     bool
	err        error
	generation uint64
}

func NewLogPreview(sink LogSink, pattern string) *LogPreview {
	p, ok := sink.(LogPreviewSink)
	f := NewLogFilter(pattern)
	if !ok || !f.Enabled() {
		return nil
	}
	return &LogPreview{sink: p, filter: f, rows: map[int][]LogLine{}}
}
func (p *LogPreview) Reset(id int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.rows, id)
}
func (p *LogPreview) Add(id int, line LogLine) error {
	if p == nil || !p.filter.Match(line.Text) {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	if p.closed {
		return nil
	}
	p.rows[id] = append(p.rows[id], line)
	if len(p.rows[id]) > 500 {
		p.rows[id][0] = LogLine{}
		p.rows[id] = p.rows[id][1:]
	}
	delay := 250*time.Millisecond - time.Since(p.last)
	if delay <= 0 {
		if p.timer != nil {
			p.timer.Stop()
			p.timer = nil
			p.generation++
		}
		return p.emit()
	}
	if p.timer == nil {
		p.generation++
		generation := p.generation
		p.timer = time.AfterFunc(delay, func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			if generation != p.generation {
				return
			}
			p.timer = nil
			if !p.closed {
				p.err = p.emit()
			}
		})
	}
	return nil
}
func (p *LogPreview) emit() error {
	ids := make([]int, 0, len(p.rows))
	for id := range p.rows {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var rows []LogPreviewLine
	for _, id := range ids {
		for _, line := range p.rows[id] {
			rows = append(rows, LogPreviewLine{id, line})
		}
	}
	rows = PreviewLogLines(rows)
	p.last = time.Now()
	if len(rows) == 0 {
		return nil
	}
	return p.sink.Preview(rows, false)
}
func (p *LogPreview) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.generation++
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.rows = nil
	return p.err
}
