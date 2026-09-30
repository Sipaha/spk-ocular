package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
)

// catalogSession is a session whose kinds change (discovery).
type catalogSession struct {
	fakeSession
	cmu       sync.Mutex
	cat       core.KindCatalog
	removed   map[string]bool
	listener  func(uint64)
	refreshes int
}

func (c *catalogSession) Kinds() []core.KindDescriptor { return c.Catalog().Kinds }
func (c *catalogSession) Catalog() core.KindCatalog {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return c.cat
}
func (c *catalogSession) OnKindsChanged(f func(uint64)) { c.cmu.Lock(); c.listener = f; c.cmu.Unlock() }
func (c *catalogSession) RefreshKinds()                 { c.cmu.Lock(); c.refreshes++; c.cmu.Unlock() }
func (c *catalogSession) KindRemoved(id string) bool {
	c.cmu.Lock()
	defer c.cmu.Unlock()
	return c.removed[id]
}

// publish changes the kinds and tells the listener, as discovery does.
func (c *catalogSession) publish(kinds []core.KindDescriptor, removed ...string) {
	c.cmu.Lock()
	c.cat = core.KindCatalog{Kinds: kinds, Rev: c.cat.Rev + 1, State: core.CatalogReady}
	for _, id := range removed {
		c.removed[id] = true
	}
	f, rev := c.listener, c.cat.Rev
	c.cmu.Unlock()
	if f != nil {
		f(rev)
	}
}

type catalogProvider struct {
	*openable
	mu       sync.Mutex
	sessions []*catalogSession
}

func (p *catalogProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	fs, _ := p.openable.Open(ctx, target)
	cs := &catalogSession{fakeSession: fakeSession{target: target, hash: fs.ConfigHash()}, removed: map[string]bool{},
		cat: core.KindCatalog{Kinds: []core.KindDescriptor{{ID: "pods", Title: "Pods", Scoped: true}}, Rev: 1, State: core.CatalogDiscovering}}
	p.mu.Lock()
	p.sessions = append(p.sessions, cs)
	p.mu.Unlock()
	return cs, nil
}

func (p *catalogProvider) last() *catalogSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions[len(p.sessions)-1]
}

func kindsEvent(t *testing.T, sub *events.Subscription) map[string]any {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Type == EventKindsChanged {
					return ev.Payload
				}
			}
		case <-deadline:
			t.Fatal("no kinds_changed event")
			return nil
		}
	}
}

// ListKinds says the catalog's revision, state and session; a new revision
// is an event the UI re-lists on.
func TestKindsCarryTheCatalogRevisionAndChangesAreEvents(t *testing.T) {
	ctx := context.Background()
	p := &catalogProvider{openable: newOpenable("a")}
	s, em := newService(t, p)
	sub, unsub := em.Subscribe()
	defer unsub()

	kv, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), kv.Rev)
	assert.Equal(t, core.CatalogDiscovering, kv.State)
	assert.Len(t, kv.Kinds, 1)
	session := kv.Session
	assert.NotZero(t, session)

	p.last().publish([]core.KindDescriptor{{ID: "pods"}, {ID: "ocular.dev/widgets"}})
	ev := kindsEvent(t, sub)
	assert.Equal(t, map[string]any{"provider": "k", "target": "a", "session": session, "rev": uint64(2)}, ev)
	kv, err = s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, uint64(2), kv.Rev)
	assert.Equal(t, core.CatalogReady, kv.State)
	assert.Len(t, kv.Kinds, 2)

	// another incarnation of the session is another identity
	p.setHash("a", "h2")
	kv, err = s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	assert.NotEqual(t, session, kv.Session)
	assert.Equal(t, uint64(1), kv.Rev)
}

// A session without a catalog has one revision, ready.
func TestKindsOfAFixedSession(t *testing.T) {
	s, _ := newService(t, newOpenable("a"))
	kv, err := s.ListKinds(context.Background(), "k", "a")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), kv.Rev)
	assert.Equal(t, core.CatalogReady, kv.State)
	assert.Equal(t, "pods", kv.Kinds[0].ID)
	require.NoError(t, s.RefreshKinds(context.Background(), "k", "a"), "nothing to read again")
}

// A kind no longer served is "removed" (do not reopen), not unsupported.
func TestOpeningARemovedKindSaysRemoved(t *testing.T) {
	ctx := context.Background()
	p := &catalogProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	p.last().publish([]core.KindDescriptor{{ID: "pods"}}, "ocular.dev/widgets")

	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "ocular.dev/widgets", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeRemoved), "%v", err)
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "never", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeUnsupported), "%v", err)
}

func TestRefreshKindsAsksTheSession(t *testing.T) {
	p := &catalogProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	require.NoError(t, s.RefreshKinds(context.Background(), "k", "a"))
	cs := p.last()
	cs.cmu.Lock()
	defer cs.cmu.Unlock()
	assert.Equal(t, 1, cs.refreshes)
}
