package views

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
)

func row(id, name, text string) core.Row {
	return core.Row{ID: id, Ref: core.Ref{Kind: "pods", Name: name, UID: id}, Cells: []core.Cell{core.TextCell(text)}, Health: core.HealthFrom(nil)}
}

func ids(rows []core.Row) []string {
	out := []string{}
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

var ready = &provider.ViewStatus{State: provider.StatusReady}

// client mimics the UI: applies pages, advances the cursor only afterwards.
type client struct {
	cursor uint64
	rows   map[string]core.Row
	status provider.ViewStatus
}

func (c *client) apply(p Page) {
	if c.rows == nil || p.Reset {
		c.rows = map[string]core.Row{}
	}
	for _, r := range p.Upserts {
		c.rows[r.ID] = r
	}
	for _, id := range p.Deleted {
		delete(c.rows, id)
	}
	c.status = p.Status
	c.cursor = p.Version
}

func (c *client) sync(v *View) Page { p := v.Since(c.cursor); c.apply(p); return p }

func TestFirstPullIsAResetWithStatus(t *testing.T) {
	v := newView("v", nil)
	p := v.Since(0)
	assert.True(t, p.Reset)
	assert.Empty(t, p.Upserts)
	assert.NotNil(t, p.Deleted)
	assert.Equal(t, provider.StatusLoading, p.Status.State)

	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1"), row("b", "b", "1")}, Status: ready})
	var c client
	p = c.sync(v)
	assert.True(t, p.Reset)
	assert.Equal(t, []string{"a", "b"}, ids(p.Upserts))
	assert.Equal(t, provider.StatusReady, c.status.State)
}

func TestIncrementalUpsertsAndDeletes(t *testing.T) {
	v := newView("v", nil)
	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1"), row("b", "b", "1"), row("c", "c", "1")}, Status: ready})
	var c client
	c.sync(v)

	v.Apply(provider.Delta{Upserts: []core.Row{row("b", "b", "2")}})
	v.Apply(provider.Delta{Deletes: []string{"c"}})
	p := c.sync(v)
	assert.False(t, p.Reset)
	assert.Equal(t, []string{"b"}, ids(p.Upserts))
	assert.Equal(t, []string{"c"}, p.Deleted)
	assert.Len(t, c.rows, 2)
	assert.Equal(t, "2", c.rows["b"].Cells[0].Text)

	p = c.sync(v) // nothing new
	assert.Empty(t, p.Upserts)
	assert.Empty(t, p.Deleted)
}

func TestNoOpUpdateDoesNotBumpVersion(t *testing.T) {
	calls := 0
	v := newView("v", func() { calls++ })
	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1")}})
	before := v.Version()
	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1")}}) // relist: identical
	v.Apply(provider.Delta{Deletes: []string{"zzz"}})                // unknown delete
	assert.Equal(t, before, v.Version())
	assert.Equal(t, 1, calls)
}

func TestStatusChangeAloneBumpsVersion(t *testing.T) {
	v := newView("v", nil)
	var c client
	c.sync(v)
	v.Apply(provider.Delta{Status: &provider.ViewStatus{State: provider.StatusError, Class: provider.ClassForbidden, Message: "pods is forbidden"}})
	p := c.sync(v)
	assert.False(t, p.Reset)
	assert.Equal(t, provider.ClassForbidden, p.Status.Class)
}

func TestResetReplacesAtomically(t *testing.T) {
	v := newView("v", nil)
	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1"), row("b", "b", "1")}})
	var c client
	c.sync(v)
	v.Apply(provider.Delta{Reset: true, Upserts: []core.Row{row("b", "b", "1"), row("x", "x", "1")}})
	p := c.sync(v)
	assert.False(t, p.Reset, "an incremental client sees the reset as changes")
	assert.Equal(t, []string{"x"}, ids(p.Upserts), "b unchanged is not resent")
	assert.Equal(t, []string{"a"}, p.Deleted)

	v.Apply(provider.Delta{Reset: true}) // empty reset is valid: everything went away
	c.sync(v)
	assert.Empty(t, c.rows)
}

// A same-named replacement has a new UID (row id): delete + add; a late
// delete of the old UID must not remove the new row.
func TestReplacementWithSameNameAndLateDelete(t *testing.T) {
	v := newView("v", nil)
	v.Apply(provider.Delta{Upserts: []core.Row{row("uid-1", "web-0", "old")}})
	var c client
	c.sync(v)
	v.Apply(provider.Delta{Upserts: []core.Row{row("uid-2", "web-0", "new")}})
	v.Apply(provider.Delta{Deletes: []string{"uid-1"}})
	v.Apply(provider.Delta{Deletes: []string{"uid-1"}}) // duplicate late delete
	c.sync(v)
	require.Len(t, c.rows, 1)
	assert.Equal(t, "new", c.rows["uid-2"].Cells[0].Text)
}

func TestCursorAheadOrForeignGetsReset(t *testing.T) {
	v := newView("v", nil)
	v.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1")}})
	p := v.Since(v.Version() + 100)
	assert.True(t, p.Reset)
	assert.Equal(t, []string{"a"}, ids(p.Upserts))
}

func TestTombstoneOverflowForcesResetForOldCursors(t *testing.T) {
	v := newView("v", nil)
	var rows []core.Row
	for i := 0; i < maxTombstones+10; i++ {
		rows = append(rows, row(fmt.Sprint(i), fmt.Sprint(i), "1"))
	}
	v.Apply(provider.Delta{Upserts: rows})
	var c client
	c.sync(v)
	var dels []string
	for i := 0; i < maxTombstones+5; i++ {
		dels = append(dels, fmt.Sprint(i))
	}
	v.Apply(provider.Delta{Deletes: dels})
	p := c.sync(v)
	assert.True(t, p.Reset, "history dropped: the old cursor gets a snapshot")
	assert.Len(t, c.rows, 5)

	v.Apply(provider.Delta{Deletes: []string{fmt.Sprint(maxTombstones + 5)}})
	p = c.sync(v)
	assert.False(t, p.Reset, "a fresh cursor is incremental again")
	assert.Len(t, c.rows, 4)
}

// Changes during a pull are not lost: the cursor is the page's version, so
// the next pull returns them.
func TestConcurrentApplyAndPullConverge(t *testing.T) {
	v := newView("v", nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			id := fmt.Sprint(i % 50)
			if i%7 == 0 {
				v.Apply(provider.Delta{Deletes: []string{id}})
			} else {
				v.Apply(provider.Delta{Upserts: []core.Row{row(id, id, fmt.Sprint(i))}})
			}
		}
	}()
	var c client
	for i := 0; i < 200; i++ {
		c.sync(v)
	}
	wg.Wait()
	c.sync(v)
	final := v.Since(0)
	require.Len(t, c.rows, len(final.Upserts))
	for _, r := range final.Upserts {
		assert.Equal(t, r, c.rows[r.ID])
	}
}

type fakeSource struct {
	mu      sync.Mutex
	sinks   []provider.Sink
	stopped int
	err     error
}

func (f *fakeSource) Watch(_ provider.Query, sink provider.Sink) (func(), error) {
	if f.err != nil {
		return nil, f.err
	}
	f.mu.Lock()
	f.sinks = append(f.sinks, sink)
	f.mu.Unlock()
	sink.Apply(provider.Delta{Upserts: []core.Row{row("a", "a", "1")}, Status: ready}) // warm cache: synchronous replay
	return func() { f.mu.Lock(); f.stopped++; f.mu.Unlock() }, nil
}

func (f *fakeSource) last() provider.Sink {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sinks[len(f.sinks)-1]
}

func TestManagerOpenGetNotifyClose(t *testing.T) {
	em := events.NewEmitter()
	sub, unsub := em.Subscribe()
	defer unsub()
	m := NewManager(em)
	defer m.CloseAll()
	src := &fakeSource{}

	id, err := m.Open("s1", src, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	require.NoError(t, err)
	p, err := m.Get(id, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, ids(p.Upserts), "the warm-cache replay is visible to the first pull")

	src.last().Apply(provider.Delta{Upserts: []core.Row{row("b", "b", "1")}})
	src.last().Apply(provider.Delta{Upserts: []core.Row{row("c", "c", "1")}})
	var got []events.Event
	require.Eventually(t, func() bool {
		select {
		case <-sub.Wake():
			got = append(got, sub.Drain()...)
		default:
		}
		for _, ev := range got {
			if ev.Type == EventViewChanged && ev.Payload["version"] == p.Version+2 {
				return true
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "one coalesced notification with the latest version")

	id2, _ := m.Open("s1", src, provider.Query{Kind: "pods", Scope: core.ScopeSel{Mode: core.ScopeAll}})
	assert.NotEqual(t, id, id2, "ids are never reused")
	assert.Equal(t, map[string]int{"s1": 2}, m.Owners())

	m.Close(id)
	_, err = m.Get(id, 0)
	assert.ErrorIs(t, err, ErrGone)
	assert.Equal(t, []string{id2}, m.CloseOwner("s1"))
	assert.Equal(t, 2, src.stopped)
	assert.Empty(t, m.Owners())
}

func TestManagerIDsDifferAcrossInstances(t *testing.T) {
	a, b := NewManager(events.NewEmitter()), NewManager(events.NewEmitter())
	defer a.CloseAll()
	defer b.CloseAll()
	ida, _ := a.Open("o", &fakeSource{}, provider.Query{})
	idb, _ := b.Open("o", &fakeSource{}, provider.Query{})
	assert.NotEqual(t, ida, idb, "a cursor from an earlier run never matches")
	_, err := b.Get(ida, 0)
	assert.ErrorIs(t, err, ErrGone)
}

func TestCloseOwnerTellsIdleClientsTheViewIsGone(t *testing.T) {
	em := events.NewEmitter()
	sub, unsub := em.Subscribe()
	defer unsub()
	m := NewManager(em)
	defer m.CloseAll()
	id, _ := m.Open("s1", &fakeSource{}, provider.Query{})
	m.CloseOwner("s1")
	require.Eventually(t, func() bool {
		select {
		case <-sub.Wake():
			for _, ev := range sub.Drain() {
				if ev.Payload["viewId"] == id && ev.Payload["gone"] == true {
					return true
				}
			}
		default:
		}
		return false
	}, 2*time.Second, 10*time.Millisecond)
}

func TestLeasesExpireUnlessTouchedOrPulled(t *testing.T) {
	m := NewManager(events.NewEmitter())
	defer m.CloseAll()
	now := time.Unix(1000, 0)
	m.now = func() time.Time { return now }
	src := &fakeSource{}
	kept, _ := m.Open("o", src, provider.Query{})
	pulled, _ := m.Open("o", src, provider.Query{})
	orphan, _ := m.Open("o", src, provider.Query{})

	now = now.Add(leaseTTL / 2)
	assert.Empty(t, m.Touch([]string{kept}))
	_, err := m.Get(pulled, 0)
	require.NoError(t, err)

	now = now.Add(leaseTTL/2 + time.Second)
	assert.Equal(t, []string{orphan}, m.Expire())
	assert.Equal(t, []string{orphan}, m.Touch([]string{kept, orphan}), "the UI learns which to reopen")
	assert.Equal(t, 1, src.stopped)
}
