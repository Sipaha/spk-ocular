package api

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// openable is a provider whose targets have a mutable config hash.
type openable struct {
	fakeProvider
	mu     sync.Mutex
	hashes map[string]string
	opened []*fakeSession
}

func (o *openable) Discover(ctx context.Context) (provider.Discovery, error) {
	d, err := o.fakeProvider.Discover(ctx)
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range d.Targets {
		d.Targets[i].ConfigHash = o.hashes[d.Targets[i].ID]
	}
	return d, err
}

func (o *openable) Open(_ context.Context, target string) (provider.Session, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s := &fakeSession{target: target, hash: o.hashes[target]}
	o.opened = append(o.opened, s)
	return s, nil
}

func (o *openable) setHash(target, h string) { o.mu.Lock(); o.hashes[target] = h; o.mu.Unlock() }

type fakeSession struct {
	target    string
	hash      string
	mu        sync.Mutex
	closed    bool
	scopesErr error
}

func (f *fakeSession) ConfigHash() string { return f.hash }
func (f *fakeSession) Kinds() []core.KindDescriptor {
	return []core.KindDescriptor{{ID: "pods", Title: "Pods", Scoped: true}}
}
func (f *fakeSession) Scopes(context.Context) ([]core.Scope, error) {
	if f.scopesErr != nil {
		return nil, f.scopesErr
	}
	return []core.Scope{{Name: "default"}}, nil
}
func (f *fakeSession) Watch(_ provider.Query, sink provider.Sink) (func(), error) {
	sink.Apply(provider.Delta{Upserts: []core.Row{{ID: "1", Ref: core.Ref{Name: "p"}}}, Status: &provider.ViewStatus{State: provider.StatusReady}})
	return func() {}, nil
}
func (f *fakeSession) Get(context.Context, core.Ref) (*core.Resource, error) { return nil, nil }
func (f *fakeSession) Close()                                                { f.mu.Lock(); f.closed = true; f.mu.Unlock() }
func (f *fakeSession) isClosed() bool                                        { f.mu.Lock(); defer f.mu.Unlock(); return f.closed }

func newOpenable(targets ...string) *openable {
	o := &openable{fakeProvider: fakeProvider{id: "k", targets: targets, changed: make(chan struct{})}, hashes: map[string]string{}}
	for _, t := range targets {
		o.hashes[t] = "h1"
	}
	return o
}

var allScopes = core.ScopeSel{Mode: core.ScopeAll}

func TestViewLifecycleThroughTheService(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, _ := newService(t, k)

	kinds, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	assert.Equal(t, "pods", kinds[0].ID)

	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	assert.Equal(t, "Pods", info.Kind.Title)
	p, err := s.GetRows(ctx, info.ViewID, 0)
	require.NoError(t, err)
	assert.Len(t, p.Upserts, 1)
	assert.Equal(t, provider.StatusReady, p.Status.State)
	assert.Len(t, k.opened, 1, "one session for the target")

	require.NoError(t, s.CloseView(ctx, info.ViewID))
	_, err = s.GetRows(ctx, info.ViewID, 0)
	assert.True(t, IsCoded(err, CodeGone))
	gone, _ := s.TouchViews(ctx, []string{info.ViewID})
	assert.Equal(t, []string{info.ViewID}, gone)
}

func TestConfigChangeClosesSessionAndTellsViews(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	s.Start(ctx)
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)

	k.setHash("a", "h2") // kubeconfig edited: same context, another server/user
	k.changed <- struct{}{}
	require.Eventually(t, func() bool {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Type == EventViewChanged && ev.Payload["gone"] == true && ev.Payload["viewId"] == info.ViewID {
					return true
				}
			}
		default:
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
	assert.True(t, k.opened[0].isClosed())

	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)
	require.Len(t, k.opened, 2)
	assert.Equal(t, "h2", k.opened[1].ConfigHash())
}

func TestSelectingAnotherTargetClosesOtherSessions(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a", "b")
	s, _ := newService(t, k)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	assert.True(t, k.opened[0].isClosed())
}

func TestScopesForbiddenIsDataNotFailure(t *testing.T) {
	ctx := context.Background()
	k := newOpenable("a")
	s, _ := newService(t, k)
	_, err := s.ListKinds(ctx, "k", "a")
	require.NoError(t, err)
	k.opened[0].scopesErr = &provider.Error{Class: provider.ClassForbidden, Message: "namespaces is forbidden"}
	v, err := s.ListScopes(ctx, "k", "a")
	require.NoError(t, err)
	require.NotNil(t, v.Error)
	assert.Equal(t, "forbidden", v.Error.Code)
	assert.NotNil(t, v.Scopes)
}

func TestOpenViewValidation(t *testing.T) {
	ctx := context.Background()
	s, _ := newService(t, newOpenable("a"))
	_, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods"}})
	assert.True(t, IsCoded(err, CodeBadRequest), "scope selector is required")
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "nope", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeUnsupported))
	_, err = s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "zzz", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	assert.True(t, IsCoded(err, CodeNotFound))
	s2, _ := newService(t, &fakeProvider{id: "plain", targets: []string{"x"}})
	_, err = s2.ListKinds(ctx, "plain", "x")
	assert.True(t, IsCoded(err, CodeUnsupported), "providers without Open")
}

type metricSession struct {
	*fakeSession
	m   provider.Metrics
	err error
}

func (m *metricSession) Metrics(context.Context, provider.Query) (provider.Metrics, error) {
	return m.m, m.err
}

type metricOpenable struct {
	*openable
	ms *metricSession
}

func (o *metricOpenable) Open(ctx context.Context, target string) (provider.Session, error) {
	s, _ := o.openable.Open(ctx, target)
	o.ms.fakeSession = s.(*fakeSession)
	return o.ms, nil
}

func TestGetMetricsJoinsCurrentRowsAndReportsStatus(t *testing.T) {
	ctx := context.Background()
	ms := &metricSession{m: provider.Metrics{Values: map[string]provider.Usage{
		"/p":         {CPU: 0.1, Memory: 1024}, // fakeSession's row: Ref{Name: "p"}, no scope
		"ns/deleted": {CPU: 9},
	}}}
	k := &metricOpenable{openable: newOpenable("a"), ms: ms}
	s, _ := newService(t, k)
	info, err := s.OpenView(ctx, OpenViewRequest{Provider: "k", Target: "a", Query: provider.Query{Kind: "pods", Scope: allScopes}})
	require.NoError(t, err)

	mv, err := s.GetMetrics(ctx, info.ViewID)
	require.NoError(t, err)
	assert.Equal(t, "ok", mv.Status)
	assert.Equal(t, map[string]provider.Usage{"1": {CPU: 0.1, Memory: 1024}}, mv.Values, "keyed by row id; objects not in the view get nothing")

	ms.err = &provider.Error{Class: provider.ClassUnsupported, Message: "metrics.k8s.io is not installed"}
	mv, err = s.GetMetrics(ctx, info.ViewID)
	require.NoError(t, err, "a missing metrics API is a status, not a failed call")
	assert.Equal(t, "unsupported", mv.Status)
	assert.Empty(t, mv.Values)

	_, err = s.GetMetrics(ctx, "v-unknown")
	assert.True(t, IsCoded(err, CodeGone))
}
