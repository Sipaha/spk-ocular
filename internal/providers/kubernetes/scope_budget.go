package kubernetes

import (
	"context"
	"io"
	"sync"

	"github.com/spk/spk-ocular/internal/provider"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/watch"
)

// Broad inventories require explicit namespace selection beyond these bounds.
// This is admission control, never a truncated successful collection.
const allNamespaceObjects = 2000
const allNamespaceBytes = 16 << 20

type scopeBudget struct {
	mu      sync.Mutex
	sizes   map[string]int
	bytes   int
	blocked error
}

func newScopeBudget() *scopeBudget    { return &scopeBudget{sizes: map[string]int{}} }
func (g *scopeBudget) failure() error { g.mu.Lock(); defer g.mu.Unlock(); return g.blocked }
func (g *scopeBudget) rejectLocked() error {
	g.blocked = &provider.Error{Class: provider.ClassScopeLimit, Message: "All Namespaces exceeds the inventory budget; select specific namespaces"}
	return g.blocked
}
func (g *scopeBudget) begin() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked != nil {
		return g.blocked
	}
	g.sizes = map[string]int{}
	g.bytes = 0
	return nil
}
func (g *scopeBudget) estimate(n int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked != nil {
		return g.blocked
	}
	if n > allNamespaceObjects {
		return g.rejectLocked()
	}
	return nil
}
func (g *scopeBudget) observe(u *unstructured.Unstructured, deleted bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked != nil {
		return g.blocked
	}
	key := u.GetNamespace() + "/" + u.GetName()
	old, exists := g.sizes[key]
	if deleted {
		delete(g.sizes, key)
		g.bytes -= old
		return nil
	}
	sizeOfObject, err := encodedJSONSize(u.Object)
	if err != nil {
		return err
	}
	count := len(g.sizes)
	if !exists {
		count++
	}
	size := g.bytes - old + sizeOfObject
	if count > allNamespaceObjects || size > allNamespaceBytes {
		return g.rejectLocked()
	}
	g.sizes[key] = sizeOfObject
	g.bytes = size
	return nil
}
func (g *scopeBudget) event(ev watch.Event, pre func(*unstructured.Unstructured)) error {
	if u, ok := ev.Object.(*unstructured.Unstructured); ok && ev.Type != watch.Bookmark {
		if err := g.observe(u, ev.Type == watch.Deleted); err != nil {
			return err
		}
		if ev.Type != watch.Deleted {
			pre(u)
		}
	}
	return nil
}

type scopeBudgetKey struct{}

func (g *scopeBudget) context(ctx context.Context) context.Context {
	return context.WithValue(ctx, scopeBudgetKey{}, g)
}

// Limit decoding even when an API implementation ignores the requested page.
// Explicit namespace reads retain their normal complete collection behavior.
type scopeBody struct {
	io.ReadCloser
	guard *scopeBudget
	read  int
}

func (b *scopeBody) Read(p []byte) (int, error) {
	const responseBytes = 8 << 20
	remaining := responseBytes - b.read
	if len(p) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	b.read += n
	if b.read > responseBytes {
		b.guard.mu.Lock()
		err = b.guard.rejectLocked()
		b.guard.mu.Unlock()
		_ = b.Close()
		return 0, err
	}
	return n, err
}
