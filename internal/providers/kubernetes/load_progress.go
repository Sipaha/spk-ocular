package kubernetes

import (
	"context"
	"io"
	"sync/atomic"
)

// loadTransfer counts response-body bytes consumed by an initial inventory.
// It includes retries and WatchList bootstrap, not subsequent live watch data.
// Bodies are counted before admission wrappers; headers/TLS are excluded.
type loadTransfer struct {
	bytes    atomic.Int64
	known    atomic.Bool
	finished atomic.Bool
}
type loadTransferKey struct{}

func (p *loadTransfer) context(ctx context.Context) context.Context {
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, loadTransferKey{}, p)
}
func (p *loadTransfer) snapshot() *int64 {
	if !p.known.Load() {
		return nil
	}
	n := p.bytes.Load()
	return &n
}

type transferBody struct {
	io.ReadCloser
	progress *loadTransfer
}

func (b *transferBody) Read(dst []byte) (int, error) {
	n, err := b.ReadCloser.Read(dst)
	if !b.progress.finished.Load() {
		b.progress.bytes.Add(int64(n))
	}
	return n, err
}
func countTransfer(ctx context.Context, body io.ReadCloser) io.ReadCloser {
	if p, ok := ctx.Value(loadTransferKey{}).(*loadTransfer); ok && body != nil {
		return &transferBody{ReadCloser: body, progress: p}
	}
	return body
}
