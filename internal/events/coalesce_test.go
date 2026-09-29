package events

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recorder struct {
	mu  sync.Mutex
	got []string
}

func (r *recorder) add(s string) func() {
	return func() {
		r.mu.Lock()
		r.got = append(r.got, s)
		r.mu.Unlock()
	}
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

func TestCoalescerBurstRunsOnceWithLatest(t *testing.T) {
	c := NewCoalescer(30 * time.Millisecond)
	defer c.Close()
	var r recorder
	for _, s := range []string{"a", "b", "c"} {
		c.Schedule("k", r.add(s))
	}
	require.Eventually(t, func() bool { return len(r.list()) == 1 }, time.Second, 5*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, []string{"c"}, r.list())
}

func TestCoalescerKeysAreIndependentAndRearm(t *testing.T) {
	c := NewCoalescer(20 * time.Millisecond)
	defer c.Close()
	var r recorder
	c.Schedule("x", r.add("x1"))
	c.Schedule("y", r.add("y1"))
	require.Eventually(t, func() bool { return len(r.list()) == 2 }, time.Second, 5*time.Millisecond)
	c.Schedule("x", r.add("x2"))
	require.Eventually(t, func() bool { return len(r.list()) == 3 }, time.Second, 5*time.Millisecond)
	assert.ElementsMatch(t, []string{"x1", "y1", "x2"}, r.list())
}

func TestCoalescerRunsSerially(t *testing.T) {
	c := NewCoalescer(time.Millisecond)
	defer c.Close()
	var mu sync.Mutex
	running, maxRunning, done := 0, 0, 0
	for i := 0; i < 20; i++ {
		c.Schedule(string(rune('a'+i)), func() {
			mu.Lock()
			running++
			maxRunning = max(maxRunning, running)
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			running--
			done++
			mu.Unlock()
		})
	}
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return done == 20 }, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, maxRunning)
}

func TestCoalescerCloseDropsPending(t *testing.T) {
	c := NewCoalescer(30 * time.Millisecond)
	var r recorder
	c.Schedule("k", r.add("late"))
	c.Close()
	c.Schedule("k", r.add("after close"))
	time.Sleep(80 * time.Millisecond)
	assert.Empty(t, r.list())
}
