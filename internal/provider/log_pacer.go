package provider

import (
	"context"
	"io"
	"sync"
	"time"
)

// LogReadPacer bounds aggregate raw live-log decoding per view, including text
// later rejected by the filter. Each reader retains at most one 32KiB block
// while waiting; TCP backpressure handles faster producers.
type LogReadPacer struct {
	mu   sync.Mutex
	next time.Time
	rate int64
}

func NewLogReadPacer(bytesPerSecond int64) *LogReadPacer {
	return &LogReadPacer{rate: max(1, bytesPerSecond)}
}
func (p *LogReadPacer) Wrap(ctx context.Context, body io.ReadCloser) io.ReadCloser {
	if p == nil {
		return body
	}
	return &pacedLogBody{ctx: ctx, body: body, pacer: p}
}

type pacedLogBody struct {
	ctx   context.Context
	body  io.ReadCloser
	pacer *LogReadPacer
}

func (r *pacedLogBody) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(buf) > 32<<10 {
		buf = buf[:32<<10]
	}
	n, err := r.body.Read(buf)
	if n == 0 {
		return n, err
	}
	p := r.pacer
	now := time.Now()
	p.mu.Lock()
	earliest := now.Add(-time.Duration(32<<10) * time.Second / time.Duration(p.rate))
	if p.next.Before(earliest) {
		p.next = earliest
	}
	p.next = p.next.Add(time.Duration(n) * time.Second / time.Duration(p.rate))
	wait := time.Until(p.next)
	p.mu.Unlock()
	if wait > 0 {
		timer := time.NewTimer(wait)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		}
	}
	return n, err
}
func (r *pacedLogBody) Close() error { return r.body.Close() }
