package api

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
)

type fakeProvider struct {
	id      string
	mu      sync.Mutex
	targets []string
	err     error
	changed chan struct{}
}

func (f *fakeProvider) ID() string    { return f.id }
func (f *fakeProvider) Title() string { return "Fake " + f.id }
func (f *fakeProvider) Discover(context.Context) (provider.Discovery, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return provider.Discovery{}, f.err
	}
	var d provider.Discovery
	for _, t := range f.targets {
		d.Targets = append(d.Targets, core.Target{Provider: f.id, ID: t, Title: t})
	}
	return d, nil
}
func (f *fakeProvider) set(ts ...string) { f.mu.Lock(); f.targets = ts; f.mu.Unlock() }

func (f *fakeProvider) Watch(ctx context.Context, onChange func()) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-f.changed:
			onChange()
		}
	}
}

func newService(t *testing.T, ps ...provider.Provider) (*Service, *events.Emitter) {
	t.Helper()
	reg, err := provider.NewRegistry(ps...)
	require.NoError(t, err)
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	em := events.NewEmitter()
	s := NewService(reg, st, em, Options{Version: "test", Mode: "browser", Getenv: func(string) string { return "" }})
	t.Cleanup(s.Close)
	return s, em
}

func TestSelectionIsRememberedAndHiddenWhileTargetIsAbsent(t *testing.T) {
	ctx := context.Background()
	k := &fakeProvider{id: "k", targets: []string{"a", "b"}}
	s, _ := newService(t, k)

	v, err := s.ListTargets(ctx)
	require.NoError(t, err)
	assert.Nil(t, v.Selected)
	require.Len(t, v.Groups, 1)
	assert.Len(t, v.Groups[0].Targets, 2)

	require.NoError(t, s.SelectTarget(ctx, "k", "b"))
	v, _ = s.ListTargets(ctx)
	assert.Equal(t, &TargetRef{Provider: "k", ID: "b"}, v.Selected)

	k.set("a") // context removed from kubeconfig
	v, _ = s.ListTargets(ctx)
	assert.Nil(t, v.Selected)

	k.set("a", "b") // and restored: the choice comes back
	v, _ = s.ListTargets(ctx)
	assert.Equal(t, &TargetRef{Provider: "k", ID: "b"}, v.Selected)
}

func TestSelectUnknownIsNotFound(t *testing.T) {
	s, _ := newService(t, &fakeProvider{id: "k", targets: []string{"a"}})
	assert.True(t, IsCoded(s.SelectTarget(context.Background(), "k", "zzz"), CodeNotFound))
	assert.True(t, IsCoded(s.SelectTarget(context.Background(), "nope", "a"), CodeNotFound))
}

func TestFailingProviderDoesNotHideOthers(t *testing.T) {
	s, _ := newService(t, &fakeProvider{id: "bad", err: errors.New("boom")}, &fakeProvider{id: "ok", targets: []string{"x"}})
	v, err := s.ListTargets(context.Background())
	require.NoError(t, err)
	require.Len(t, v.Groups, 2)
	assert.Equal(t, "boom", v.Groups[0].Error)
	assert.NotNil(t, v.Groups[0].Targets, "empty slices, not null, for the UI")
	assert.Len(t, v.Groups[1].Targets, 1)
}

func TestWatcherChangeEmitsTargetsChanged(t *testing.T) {
	k := &fakeProvider{id: "k", changed: make(chan struct{})}
	s, em := newService(t, k)
	sub, unsub := em.Subscribe()
	defer unsub()
	s.Start(context.Background())
	k.changed <- struct{}{}
	select {
	case <-sub.Wake():
		evs := sub.Drain()
		require.Len(t, evs, 1)
		assert.Equal(t, EventTargetsChanged, evs[0].Type)
		assert.Equal(t, "k", evs[0].Payload["provider"])
	case <-time.After(2 * time.Second):
		t.Fatal("no targets_changed")
	}
}

func TestUILanguage(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want string
	}{
		{nil, "en"},
		{map[string]string{"LANG": "ru_RU.UTF-8"}, "ru"},
		{map[string]string{"LANG": "ru_RU.UTF-8", "LC_MESSAGES": "en_US.UTF-8"}, "en"},
		{map[string]string{"LANGUAGE": "ru:en", "LANG": "en_US.UTF-8"}, "ru"},
		{map[string]string{"LC_ALL": "C", "LANG": "ru_RU.UTF-8"}, "en"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, uiLanguage(func(k string) string { return c.env[k] }), c.env)
	}
}
