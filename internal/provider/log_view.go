package provider

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// StreamFilteredLogView captures new records while a finite filtered history
// scan runs. This is a UI transport contract; agent archive reads stay separate.
func StreamFilteredLogView(ctx context.Context, q LogQuery, run func(context.Context, LogQuery, LogSink) error, sink LogSink) error {
	if !q.Follow || !NewLogFilter(q.Filter).Enabled() {
		return run(ctx, q, sink)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	view := &logViewMerge{ctx: ctx, sink: sink, ids: map[string]int{}, historyIDs: map[int]int{}, liveIDs: map[int]int{}, duplicates: map[logRecordKey]int{}, closedSources: map[int]bool{}}
	started := make(chan struct{})
	liveDone := make(chan error, 1)
	live := q
	live.LiveOnly = true
	live.LivePacer = NewLogReadPacer(1 << 20)
	go func() { liveDone <- run(ctx, live, &logViewInput{view: view, live: true, started: started}) }()
	defer view.stopPreview()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-liveDone
		}
	}()
	// Discover/capture live sources before requesting the finite history. A
	// provider failure must not leave history waiting on a nonexistent follower.
	select {
	case <-started:
	case err := <-liveDone:
		joined = true
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	history := q
	history.Follow = false
	hctx, stop := context.WithTimeout(ctx, 30*time.Second)
	err := run(hctx, history, &logViewInput{view: view})
	stop()
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if err != nil {
		if e := sink.State(0, LogState{State: LogTruncated, Message: "history loading reached its deadline; the live stream continues"}); e != nil {
			return e
		}
	}
	if err = view.finish(); err != nil {
		return err
	}
	if joined {
		return nil
	}
	select {
	case err = <-liveDone:
		joined = true
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type logRecordKey struct {
	id       int
	ts, text string
	flags    int
}

func recordKey(row LogPreviewLine) logRecordKey {
	return logRecordKey{row.Source, row.Line.TS, row.Line.Text, row.Line.Flags}
}

type logViewMerge struct {
	mu                  sync.Mutex
	ctx                 context.Context
	sink                LogSink
	previewTimer        *time.Timer
	ids                 map[string]int
	historyIDs, liveIDs map[int]int
	next                int
	history, live       []LogPreviewLine
	liveBytes           int
	liveStart           int
	liveCut             bool
	done                bool
	previewAt           time.Time
	duplicates          map[logRecordKey]int
	closedSources       map[int]bool
	previewErr          error
}

type logViewInput struct {
	view    *logViewMerge
	live    bool
	started chan struct{}
	once    sync.Once
}

func (s *logViewInput) Source(id int, key, label, channel string) error {
	v := s.view
	v.mu.Lock()
	defer v.mu.Unlock()
	to, exists := v.ids[key]
	if !exists {
		v.next++
		to = v.next
		v.ids[key] = to
		if err := v.sink.Source(to, key, label, channel); err != nil {
			return err
		}
	}
	if s.live {
		v.liveIDs[id] = to
	} else {
		v.historyIDs[id] = to
	}
	return nil
}
func (s *logViewInput) mapID(id int) int {
	if id == 0 {
		return 0
	}
	if s.live {
		return s.view.liveIDs[id]
	}
	return s.view.historyIDs[id]
}
func (s *logViewInput) State(id int, st LogState) error {
	v := s.view
	v.mu.Lock()
	defer v.mu.Unlock()
	to := s.mapID(id)
	if !s.live && (st.State == LogEnded || st.State == LogStreaming) {
		return nil
	}
	if id != 0 && to == 0 {
		return fmt.Errorf("unknown log source %d", id)
	}
	err := v.sink.State(to, st)
	if s.live && to != 0 && st.State == LogEnded {
		v.closedSources[to] = true
		if v.done {
			v.pruneSource(to)
		}
	}
	return err
}
func (s *logViewInput) Ready() error {
	if s.live {
		s.once.Do(func() { close(s.started) })
	}
	return nil
}
func (s *logViewInput) Lines(id int, lines []LogLine) error {
	v := s.view
	v.mu.Lock()
	defer v.mu.Unlock()
	to := s.mapID(id)
	if to == 0 {
		return fmt.Errorf("unknown log source %d", id)
	}
	if !s.live {
		for _, line := range lines {
			v.history = append(v.history, LogPreviewLine{to, line})
		}
		return nil
	}
	if v.done {
		out := make([]LogLine, 0, len(lines))
		for _, line := range lines {
			key := logRecordKey{to, line.TS, line.Text, line.Flags}
			if line.TS != "" && v.duplicates[key] > 0 {
				v.duplicates[key]--
				if v.duplicates[key] == 0 {
					delete(v.duplicates, key)
				}
				continue
			}
			// Once a genuinely new record is observed, its source's historical
			// overlap has ended. Do not keep an unbounded fingerprint registry.
			for old := range v.duplicates {
				if old.id == to {
					delete(v.duplicates, old)
				}
			}
			out = append(out, line)
		}
		if len(out) > 0 {
			return v.sink.Lines(to, out)
		}
		return nil
	}
	for _, line := range lines {
		v.live = append(v.live, LogPreviewLine{to, line})
		v.liveBytes += len(line.Text) + len(line.TS) + 64
	}
	wasCut := v.liveCut
	for len(v.live)-v.liveStart > 50_000 || (v.liveBytes > 16<<20 && len(v.live)-v.liveStart > 1) {
		old := v.live[v.liveStart]
		v.liveBytes -= len(old.Line.Text) + len(old.Line.TS) + 64
		v.live[v.liveStart] = LogPreviewLine{}
		v.liveStart++
		v.liveCut = true
	}
	if v.liveStart >= len(v.live)/2 && v.liveStart > 0 {
		v.live = append([]LogPreviewLine(nil), v.live[v.liveStart:]...)
		v.liveStart = 0
	}
	if !wasCut && v.liveCut {
		if err := v.sink.State(0, LogState{State: LogGap, Message: "new matching logs exceeded the bounded history/live buffer; some records were omitted"}); err != nil {
			return err
		}
	}
	return v.preview(false)
}
func (s *logViewInput) Preview(lines []LogPreviewLine, final bool) error {
	if s.live {
		for i := 0; i < len(lines); {
			id := lines[i].Source
			var batch []LogLine
			size := 0
			for i < len(lines) && lines[i].Source == id && len(batch) < 500 && size < 64<<10 {
				batch = append(batch, lines[i].Line)
				size += len(lines[i].Line.Text)
				i++
			}
			if err := s.Lines(id, batch); err != nil {
				return err
			}
		}
		return nil
	}
	v := s.view
	v.mu.Lock()
	defer v.mu.Unlock()
	rows := make([]LogPreviewLine, 0, len(lines))
	for _, row := range lines {
		to := s.mapID(row.Source)
		if to == 0 {
			return fmt.Errorf("unknown preview source %d", row.Source)
		}
		row.Source = to
		rows = append(rows, row)
	}
	v.history = rows
	if final {
		return nil
	}
	return v.preview(false)
}

// union preserves the provider's history order and live arrival order. Only
// timestamped overlap is removed, by multiplicity (identical lines can repeat).
func logUnion(history, live []LogPreviewLine) ([]LogPreviewLine, map[logRecordKey]int) {
	remaining := map[logRecordKey]int{}
	for _, row := range history {
		if row.Line.TS != "" {
			remaining[recordKey(row)]++
		}
	}
	rows := append([]LogPreviewLine(nil), history...)
	for _, row := range live {
		key := recordKey(row)
		if row.Line.TS != "" && remaining[key] > 0 {
			remaining[key]--
			if remaining[key] == 0 {
				delete(remaining, key)
			}
			continue
		}
		rows = append(rows, row)
	}
	return rows, remaining
}
func (v *logViewMerge) preview(force bool) error {
	if v.previewErr != nil {
		return v.previewErr
	}
	preview, ok := v.sink.(LogPreviewSink)
	if !ok {
		return nil
	}
	if !force && time.Since(v.previewAt) < 250*time.Millisecond {
		if v.previewTimer == nil {
			v.previewTimer = time.AfterFunc(250*time.Millisecond-time.Since(v.previewAt), func() {
				v.mu.Lock()
				defer v.mu.Unlock()
				v.previewTimer = nil
				if !v.done && v.ctx.Err() == nil {
					v.previewErr = v.preview(false)
				}
			})
		}
		return nil
	}
	history, live := PreviewLogLines(v.history), PreviewLogLines(v.live[v.liveStart:])
	rows, _ := logUnion(history, live)
	rows = PreviewLogLines(rows)
	if len(rows) == 0 && !force {
		return nil
	}
	v.previewAt = time.Now()
	return preview.Preview(rows, false)
}
func (v *logViewMerge) finish() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.previewErr != nil {
		return v.previewErr
	}
	if v.previewTimer != nil {
		v.previewTimer.Stop()
		v.previewTimer = nil
	}
	untimed := map[int]bool{}
	ambiguous := false
	for _, row := range v.history {
		if row.Line.TS == "" {
			untimed[row.Source] = true
		}
	}
	for _, row := range v.live[v.liveStart:] {
		if row.Line.TS == "" && untimed[row.Source] {
			ambiguous = true
			break
		}
	}
	rows, remaining := logUnion(v.history, v.live[v.liveStart:])
	bytes, start := 0, len(rows)
	for start > 0 && len(rows)-start < 50_000 {
		row := rows[start-1]
		size := len(row.Line.Text) + len(row.Line.TS) + 64
		if bytes+size > 16<<20 {
			break
		}
		bytes += size
		start--
	}
	if start > 0 {
		rows = rows[start:]
		v.liveCut = true
	}
	if p, ok := v.sink.(LogPreviewSink); ok {
		if err := p.Preview(rows, true); err != nil {
			return err
		}
	} else {
		for _, row := range rows {
			if err := v.sink.Lines(row.Source, []LogLine{row.Line}); err != nil {
				return err
			}
		}
	}
	if v.liveCut {
		if err := v.sink.State(0, LogState{State: LogGap, Message: "new matching logs exceeded the bounded history/live buffer; some records were omitted"}); err != nil {
			return err
		}
	}
	if ambiguous {
		if err := v.sink.State(0, LogState{State: LogGap, Message: "some records have no timestamp; history/live overlap cannot be verified and may repeat"}); err != nil {
			return err
		}
	}
	v.history = nil
	v.live = nil
	v.liveBytes = 0
	v.liveStart = 0
	v.duplicates = remaining
	v.done = true
	v.historyIDs = nil
	for id := range v.closedSources {
		v.pruneSource(id)
	}
	return v.sink.Ready()
}

func (v *logViewMerge) stopPreview() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.done = true
	if v.previewTimer != nil {
		v.previewTimer.Stop()
		v.previewTimer = nil
	}
}

func (v *logViewMerge) pruneSource(id int) {
	for key, to := range v.ids {
		if to == id {
			delete(v.ids, key)
		}
	}
	for key, to := range v.liveIDs {
		if to == id {
			delete(v.liveIDs, key)
		}
	}
	for key := range v.duplicates {
		if key.id == id {
			delete(v.duplicates, key)
		}
	}
	delete(v.closedSources, id)
}
