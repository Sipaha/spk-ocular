package engine_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

func TestEventsDecodeAndCarrySinceAndFilters(t *testing.T) {
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	since := time.Unix(1790000000, 5000)
	filters := engine.Filters{"type": {"container"}, "event": {"start", "die"}}
	s, err := c.Events(context.Background(), since, filters)
	require.NoError(t, err)
	defer s.Close()
	f.WaitEventSubscribers(t, 1)

	reqs := f.Requests()
	q := reqs[len(reqs)-1].Query
	assert.Equal(t, "1790000000.000005000", q.Get("since"))
	var sent map[string][]string
	require.NoError(t, json.Unmarshal([]byte(q.Get("filters")), &sent))
	assert.Equal(t, map[string][]string(filters), sent)

	// Docker 29's wire form, with the legacy fields the client ignores
	f.EmitRaw([]byte(`{"status":"die","id":"c1","from":"busybox","Type":"container","Action":"die",` +
		`"Actor":{"ID":"c1","Attributes":{"exitCode":"3","name":"fixture-web-1","com.docker.compose.project":"fixture"}},` +
		`"scope":"local","time":1790000001,"timeNano":1790000001123456789}` + "\n\n"))
	ev, err := s.Next()
	require.NoError(t, err)
	assert.Equal(t, engine.Event{
		Type: "container", Action: "die", Scope: "local", Time: 1790000001, TimeNano: 1790000001123456789,
		Actor: engine.EventActor{ID: "c1", Attributes: map[string]string{
			"exitCode": "3", "name": "fixture-web-1", "com.docker.compose.project": "fixture"}},
	}, ev)
	assert.Equal(t, time.Unix(0, 1790000001123456789), ev.When())

	f.Emit(engine.Event{Type: "container", Action: "start", Actor: engine.EventActor{ID: "c2"}})
	f.Emit(engine.Event{Type: "network", Action: "connect", Actor: engine.EventActor{ID: "n1"}}) // filtered out by the fake
	f.Emit(engine.Event{Type: "container", Action: "die", Actor: engine.EventActor{ID: "c3"}})
	for _, want := range []string{"c2", "c3"} {
		ev, err := s.Next()
		require.NoError(t, err)
		assert.Equal(t, want, ev.Actor.ID)
	}

	f.EndEvents()
	_, err = s.Next()
	assert.ErrorIs(t, err, io.EOF)
}

func TestEventsSinceReplaysHistory(t *testing.T) {
	f := enginefake.New(t)
	f.Emit(engine.Event{Type: "container", Action: "create", Actor: engine.EventActor{ID: "old"}, TimeNano: time.Now().Add(-time.Minute).UnixNano()})
	f.Emit(engine.Event{Type: "container", Action: "create", Actor: engine.EventActor{ID: "recent"}})
	c := newClient(t, engine.Config{Host: f.Host()})
	s, err := c.Events(context.Background(), time.Now().Add(-10*time.Second), nil)
	require.NoError(t, err)
	defer s.Close()
	ev, err := s.Next()
	require.NoError(t, err)
	assert.Equal(t, "recent", ev.Actor.ID)
}

func TestAnOverlongEventEndsTheStream(t *testing.T) {
	// Skipping it would hide a change from the observer; the stream ends
	// with ErrEventTooLarge and the observer resubscribes and resyncs.
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host(), Limits: engine.Limits{EventLineBytes: 256}})
	s, err := c.Events(context.Background(), time.Time{}, nil)
	require.NoError(t, err)
	defer s.Close()
	f.WaitEventSubscribers(t, 1)

	f.Emit(engine.Event{Type: "container", Action: "start", Actor: engine.EventActor{ID: "fits"}})
	f.Emit(engine.Event{Type: "container", Action: "start", Actor: engine.EventActor{ID: strings.Repeat("x", 100000)}})
	ev, err := s.Next()
	require.NoError(t, err)
	assert.Equal(t, "fits", ev.Actor.ID)
	_, err = s.Next()
	assert.ErrorIs(t, err, engine.ErrEventTooLarge)
	assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
}

func TestABrokenEventIsAnError(t *testing.T) {
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	s, err := c.Events(context.Background(), time.Time{}, nil)
	require.NoError(t, err)
	defer s.Close()
	f.WaitEventSubscribers(t, 1)
	f.EmitRaw([]byte("{not json\n"))
	_, err = s.Next()
	assert.Equal(t, provider.ClassUnavailable, engine.ClassOf(err))
}

func TestAnEventCutByTheEndOfTheStreamIsAnError(t *testing.T) {
	f := enginefake.New(t)
	c := newClient(t, engine.Config{Host: f.Host()})
	s, err := c.Events(context.Background(), time.Time{}, nil)
	require.NoError(t, err)
	defer s.Close()
	f.WaitEventSubscribers(t, 1)
	f.EmitRaw([]byte(`{"Type":"container","Act`))
	f.EndEvents()
	_, err = s.Next()
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestEventsErrorAnswerIsClassified(t *testing.T) {
	f := enginefake.New(t)
	f.AddHook(enginefake.Fail("/events", 403, "events: access denied by authz plugin"))
	c := newClient(t, engine.Config{Host: f.Host()})
	_, err := c.Events(context.Background(), time.Time{}, nil)
	e := engineError(t, err)
	assert.Equal(t, provider.ClassForbidden, e.Class)
	assert.Equal(t, "events: access denied by authz plugin", e.Message)
}
