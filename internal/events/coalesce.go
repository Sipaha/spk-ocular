package events

import (
	"sync"
	"time"
)

// Coalescer turns bursts of "something changed" into one call per key: the
// first Schedule for a key arms a timer, later ones before it fires only
// replace the function (the latest wins). Latency is bounded by the delay —
// a steady stream of changes still produces a call every delay. Functions
// run one at a time.
type Coalescer struct {
	delay  time.Duration
	mu     sync.Mutex
	run    sync.Mutex
	jobs   map[string]*job
	closed bool
}

type job struct {
	fn func()
	t  *time.Timer
}

func NewCoalescer(delay time.Duration) *Coalescer {
	return &Coalescer{delay: delay, jobs: map[string]*job{}}
}

func (c *Coalescer) Schedule(key string, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if j, ok := c.jobs[key]; ok {
		j.fn = fn
		return
	}
	j := &job{fn: fn}
	c.jobs[key] = j
	j.t = time.AfterFunc(c.delay, func() { c.fire(key, j) })
}

func (c *Coalescer) fire(key string, j *job) {
	c.run.Lock()
	defer c.run.Unlock()
	c.mu.Lock()
	if c.closed || c.jobs[key] != j {
		c.mu.Unlock()
		return
	}
	delete(c.jobs, key) // a Schedule during fn arms a new job: no update is lost
	fn := j.fn
	c.mu.Unlock()
	fn()
}

// Close drops pending calls and waits for a running one to finish.
func (c *Coalescer) Close() {
	c.mu.Lock()
	c.closed = true
	for _, j := range c.jobs {
		j.t.Stop()
	}
	c.jobs = map[string]*job{}
	c.mu.Unlock()
	c.run.Lock() // waits for a running fn; fires after this see closed and return
	defer c.run.Unlock()
}
