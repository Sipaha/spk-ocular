package provider

import (
	"context"
	"io"
	"sync/atomic"
	"time"
)

// DownloadCounter measures consumed response payload, never retained object size.
// A completed snapshot remains identifiable so a later warm view reports zero.
type DownloadCounter struct {
	bytes     atomic.Int64
	completed atomic.Int64
}
type downloadCounterKey struct{}

func (p *DownloadCounter) Context(ctx context.Context) context.Context {
	return context.WithValue(ctx, downloadCounterKey{}, p)
}
func (p *DownloadCounter) Complete() { p.completed.CompareAndSwap(0, time.Now().UnixNano()) }
func (p *DownloadCounter) Since(start time.Time) int64 {
	if end := p.completed.Load(); end != 0 && end < start.UnixNano() {
		return 0
	}
	return p.bytes.Load()
}

type downloadBody struct {
	io.ReadCloser
	counter *DownloadCounter
}

func (b *downloadBody) Read(dst []byte) (int, error) {
	n, err := b.ReadCloser.Read(dst)
	if b.counter.completed.Load() == 0 {
		b.counter.bytes.Add(int64(n))
	}
	return n, err
}
func TrackDownloadBody(ctx context.Context, body io.ReadCloser) io.ReadCloser {
	if p, ok := ctx.Value(downloadCounterKey{}).(*DownloadCounter); ok && body != nil {
		return &downloadBody{ReadCloser: body, counter: p}
	}
	return body
}

// LoadingProgressReporter reads local counters only, including while a refresh
// preparer is waiting for a new snapshot. It must never perform network I/O.
type LoadingProgressReporter interface{ LoadingProgress(Query, time.Time) *int64 }
