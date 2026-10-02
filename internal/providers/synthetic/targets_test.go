package synthetic

import (
	"context"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func TestSecondTargetIsDiscoveredOnlyWhenEnabled(t *testing.T) {
	p := New()
	d, err := p.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 1)
	_, err = p.Open(context.Background(), Target2)
	require.Error(t, err, "demo2 is refused while the second target is off")

	p.EnableSecondTarget()
	d, err = p.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, d.Targets, 2)
	assert.Equal(t, Target, d.Targets[0].ID)
	assert.Equal(t, Target2, d.Targets[1].ID)
	_, err = p.Open(context.Background(), Target2)
	require.NoError(t, err)
}

// lineSink collects streamed lines (the stream runs in its own goroutine).
type lineSink struct {
	mu    sync.Mutex
	lines []string
	ready chan struct{}
}

func (s *lineSink) Source(int, string, string, string) error { return nil }
func (s *lineSink) Lines(_ int, l []provider.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range l {
		s.lines = append(s.lines, x.Text)
	}
	return nil
}
func (s *lineSink) has(text string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.lines, text)
}
func (s *lineSink) State(int, provider.LogState) error { return nil }
func (s *lineSink) Ready() error {
	close(s.ready)
	return nil
}

func TestSecondTargetHasItsOwnRowsAndLogFeed(t *testing.T) {
	p := New()
	p.EnableSecondTarget()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s1, err := p.Open(ctx, Target)
	require.NoError(t, err)
	s2, err := p.Open(ctx, Target2)
	require.NoError(t, err)

	w1 := &rowSink{}
	_, err = s1.Watch(provider.Query{Kind: Kind}, w1)
	require.NoError(t, err)
	w2 := &rowSink{}
	_, err = s2.Watch(provider.Query{Kind: Kind}, w2)
	require.NoError(t, err)
	names := func(s *rowSink) (out []string) {
		for _, r := range s.rows {
			out = append(out, r.Ref.Target+"/"+r.Ref.Name)
		}
		sort.Strings(out)
		return out
	}
	assert.Equal(t, []string{"demo/api", "demo/workers"}, names(w1))
	assert.Equal(t, []string{"demo2/api", "demo2/workers"}, names(w2))

	// Log feeds are per target: a push to demo/api reaches no demo2 stream.
	l1 := &lineSink{ready: make(chan struct{})}
	l2 := &lineSink{ready: make(chan struct{})}
	done := make(chan error, 2)
	defer func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("log stream did not stop")
			}
		}
	}()
	go func() {
		done <- s1.(provider.LogSource).StreamLogs(ctx, core.Ref{Provider: ID, Target: Target, Kind: Kind, Name: "api"}, provider.LogQuery{Follow: true}, l1)
	}()
	go func() {
		done <- s2.(provider.LogSource).StreamLogs(ctx, core.Ref{Provider: ID, Target: Target2, Kind: Kind, Name: "api"}, provider.LogQuery{Follow: true}, l2)
	}()
	for _, ready := range []chan struct{}{l1.ready, l2.ready} {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("log stream did not become ready")
		}
	}

	// Ready precedes subscription. Wait for registration without emitting
	// probe lines, then send one terminating event to each target. Once both
	// streams finish, no late cross-target delivery can escape the assertions.
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs) == 2
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, p.Emit(Target, "api", Event{Source: -1, Lines: []string{"line on demo"}, End: true}))
	assert.Equal(t, 1, p.Emit(Target2, "api", Event{Source: -1, Lines: []string{"line on demo2"}, End: true}))
	require.Eventually(t, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return len(p.subs) == 0
	}, 5*time.Second, 5*time.Millisecond)
	assert.True(t, l1.has("line on demo"))
	assert.False(t, l1.has("line on demo2"))
	assert.True(t, l2.has("line on demo2"))
	assert.False(t, l2.has("line on demo"))
}
