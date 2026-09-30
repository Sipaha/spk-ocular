package compose

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
	"github.com/spk/spk-ocular/internal/providers/compose/enginefake"
)

// Feed tests observe the feeds through simple projections: one row per
// object of the kind's first feed, Rev = the object's name and state, so
// what a test sees is what the feeds hold.

func rawRows(q provider.Query, w *World, _ time.Time) ([]core.Row, time.Time) {
	var rows []core.Row
	add := func(id, rev string) {
		rows = append(rows, core.Row{ID: id, Rev: rev, Ref: core.Ref{Provider: ProviderID, Kind: q.Kind, Name: id}})
	}
	switch q.Kind {
	case KindContainers, KindServices:
		for id, c := range w.Containers {
			add(id, c.Name+"|"+c.State.Status+"|"+netNames(c))
		}
	case KindNetworks:
		for id, n := range w.Networks {
			add(id, n.Name+"|"+fmt.Sprint(len(n.Containers)))
		}
	case KindVolumes:
		for name, v := range w.Volumes {
			add(name, v.CreatedAt)
		}
	case KindImages:
		for id, i := range w.Images {
			add(id, strings.Join(i.RepoTags, ","))
		}
	}
	return rows, time.Time{}
}

func netNames(c *engine.ContainerInspect) string {
	var ns []string
	for n := range c.NetworkSettings.Networks {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return strings.Join(ns, ",")
}

var rawProjections = projections{
	rows: rawRows,
	resource: func(ref core.Ref, w *World, _ time.Time) (*core.Resource, error) {
		c, ok := w.Containers[ref.Name]
		if !ok {
			return nil, &provider.Error{Class: provider.ClassNotFound, Message: "no " + ref.Name}
		}
		return &core.Resource{Ref: ref, YAML: string(c.Raw)}, nil
	},
	scopes: func(w *World) []core.Scope {
		seen := map[string]bool{}
		for _, c := range w.Containers {
			seen[c.Config.Labels[LabelProject]] = true
		}
		for _, v := range w.Volumes {
			if p := v.Labels[LabelProject]; p != "" {
				seen[p] = true
			}
		}
		var out []core.Scope
		for p := range seen {
			out = append(out, core.Scope{Name: p})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	},
}

type testEnv struct {
	t  *testing.T
	fe *enginefake.Engine
	s  *session
}

func newTestEnv(t *testing.T, opts ...func(*engine.Config)) *testEnv {
	t.Helper()
	fe := enginefake.New(t)
	cfg := engine.Config{Host: fe.Host(), RequestTimeout: 2 * time.Second}
	for _, o := range opts {
		o(&cfg)
	}
	cl, err := engine.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	s := newSession("context:test", "h", cl)
	s.proj = rawProjections
	s.staleAfter = 200 * time.Millisecond
	s.backoff = []time.Duration{20 * time.Millisecond, 50 * time.Millisecond}
	t.Cleanup(s.Close)
	return &testEnv{t: t, fe: fe, s: s}
}

func composeContainer(id, project, service, state string) engine.ContainerInspect {
	return engine.ContainerInspect{
		ID: id, Name: "/" + project + "-" + service + "-" + id[:4],
		State: engine.ContainerState{Status: state, Running: state == "running"},
		Config: engine.ContainerConfig{Labels: map[string]string{
			LabelProject: project, LabelService: service, LabelNumber: "1", LabelOneoff: "False",
		}},
	}
}

func containerEvent(action string, c engine.ContainerInspect) engine.Event {
	attrs := map[string]string{"name": strings.TrimPrefix(c.Name, "/")}
	for k, v := range c.Config.Labels {
		attrs[k] = v
	}
	return engine.Event{Type: "container", Action: action, Actor: engine.EventActor{ID: c.ID, Attributes: attrs}}
}

// sink records deliveries and keeps the resulting view.
type sink struct {
	mu     sync.Mutex
	rows   map[string]core.Row
	status provider.ViewStatus
	deltas []provider.Delta
	resets int
	ch     chan struct{}
}

func newSink() *sink { return &sink{rows: map[string]core.Row{}, ch: make(chan struct{}, 1)} }

func (s *sink) Apply(d provider.Delta) {
	s.mu.Lock()
	s.deltas = append(s.deltas, d)
	if d.Reset {
		s.resets++
		s.rows = map[string]core.Row{}
	}
	for _, r := range d.Upserts {
		s.rows[r.ID] = r
	}
	for _, id := range d.Deletes {
		delete(s.rows, id)
	}
	if d.Status != nil {
		s.status = d.Status.Clone()
	}
	s.mu.Unlock()
	select {
	case s.ch <- struct{}{}:
	default:
	}
}

// waitFor waits until cond holds for the view (checked on every delivery).
func (s *sink) waitFor(t *testing.T, what string, cond func(rows map[string]core.Row, st provider.ViewStatus) bool) {
	t.Helper()
	s.waitWithin(t, 5*time.Second, what, cond)
}

func (s *sink) waitWithin(t *testing.T, d time.Duration, what string, cond func(rows map[string]core.Row, st provider.ViewStatus) bool) {
	t.Helper()
	deadline := time.After(d)
	for {
		s.mu.Lock()
		ok := cond(s.rows, s.status)
		rows, st := len(s.rows), s.status
		s.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-s.ch:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; view: %d rows, status %+v", what, rows, st)
		}
	}
}

func (s *sink) row(id string) (core.Row, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	return r, ok
}

func ready(_ map[string]core.Row, st provider.ViewStatus) bool {
	return st.State == provider.StatusReady
}

func hasRows(ids ...string) func(map[string]core.Row, provider.ViewStatus) bool {
	return func(rows map[string]core.Row, st provider.ViewStatus) bool {
		if st.State != provider.StatusReady || len(rows) != len(ids) {
			return false
		}
		for _, id := range ids {
			if _, ok := rows[id]; !ok {
				return false
			}
		}
		return true
	}
}

func (e *testEnv) watch(kind string) *sink {
	e.t.Helper()
	sk := newSink()
	stop, err := e.s.Watch(provider.Query{Kind: kind, Scope: core.ScopeSel{Mode: core.ScopeAll}}, sk)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(stop)
	return sk
}

func id(n int) string { return fmt.Sprintf("%04d%060d", n, n) }

func TestFeedSnapshotThenEvents(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	foreign := engine.ContainerInspect{ID: id(9), Name: "/lonely", Config: engine.ContainerConfig{Labels: map[string]string{}}}
	e.fe.PutContainer(foreign)

	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	sk.mu.Lock()
	first := sk.deltas
	sk.mu.Unlock()
	// The first rows come as a Reset together with or before Ready.
	if !first[len(first)-1].Reset && sk.resets != 1 {
		t.Fatalf("no Reset before Ready: %+v", first)
	}

	b := composeContainer(id(2), "p", "db", "created")
	e.fe.PutContainer(b)
	e.fe.Emit(containerEvent("create", b))
	sk.waitFor(t, "the created container", hasRows(a.ID, b.ID))

	b.State.Status, b.State.Running = "running", true
	e.fe.PutContainer(b)
	e.fe.Emit(containerEvent("start", b))
	sk.waitFor(t, "the started container", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return strings.Contains(rows[b.ID].Rev, "|running|")
	})

	before := e.fe.Count("/containers/" + foreign.ID + "/json")
	e.fe.Emit(containerEvent("start", foreign)) // no Compose labels: not read
	e.fe.RemoveContainer(a.ID)
	e.fe.Emit(containerEvent("destroy", a))
	sk.waitFor(t, "the destroyed container gone", hasRows(b.ID))
	if n := e.fe.Count("/containers/" + foreign.ID + "/json"); n != before {
		t.Fatalf("a container without Compose labels was inspected %d times", n-before)
	}
}

// Events are hints: a destroy of an object that exists again (a late or
// repeated event) does not remove it — the inspect decides.
func TestFeedOldDestroyDoesNotRemove(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	e.fe.Emit(containerEvent("destroy", a))
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(b)
	e.fe.Emit(containerEvent("create", b))
	sk.waitFor(t, "b", hasRows(a.ID, b.ID))
}

// More events than the daemon's history (256) arrive while the list is
// held: the continuous reader has them all as dirty ids.
func TestFeedManyEventsDuringSnapshot(t *testing.T) {
	e := newTestEnv(t)
	release := make(chan struct{})
	e.fe.AddHook(enginefake.StallHeaders("/containers/json", release))
	sk := e.watch(KindContainers)
	e.fe.WaitEventSubscribers(t, 1)
	var want []string
	for i := 1; i <= 300; i++ {
		c := composeContainer(id(i), "p", "web", "running")
		e.fe.PutContainer(c)
		e.fe.Emit(containerEvent("create", c))
		want = append(want, c.ID)
	}
	// The list answers without them (it was taken before): only the
	// events can bring them.
	e.fe.ClearHooks()
	e.fe.AddHook(enginefake.Body("/containers/json", []byte("[]")))
	close(release)
	sk.waitFor(t, "300 containers", hasRows(want...))
}

// Created and destroyed while the list is answered: the created one comes
// by its event, the destroyed one (in the list) is 404 on inspect.
func TestFeedCreateDestroyDuringList(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(a)
	e.fe.PutContainer(b)
	c := composeContainer(id(3), "p", "cache", "running")
	var once sync.Once
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, path string) bool {
		if path != "/containers/json" {
			return false
		}
		handled := false
		once.Do(func() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"Id":%q},{"Id":%q}]`, a.ID, b.ID)
			e.fe.RemoveContainer(b.ID)
			e.fe.Emit(containerEvent("destroy", b))
			e.fe.PutContainer(c)
			e.fe.Emit(containerEvent("create", c))
			handled = true
		})
		return handled
	})
	sk := e.watch(KindContainers)
	sk.waitFor(t, "a and c", hasRows(a.ID, c.ID))
}

// The events stream breaks: the rows stay, the view turns stale after
// staleAfter, and a new epoch's snapshot restores it (deletions included).
func TestFeedStreamBreakStaleAndRecovery(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(a)
	e.fe.PutContainer(b)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID, b.ID))

	e.fe.AddHook(enginefake.Fail("/info", 500, "daemon is restarting"))
	e.fe.EndEvents()
	sk.waitFor(t, "stale", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusStale && len(rows) == 2 && st.Class == provider.ClassUnavailable
	})
	// While down: b removed silently (its event lost with the stream).
	e.fe.RemoveContainer(b.ID)
	e.fe.ClearHooks()
	sk.waitFor(t, "recovered without b", hasRows(a.ID))
	if sk.resets != 1 {
		t.Fatalf("recovery reset the view %d times; it must apply a difference", sk.resets)
	}
}

// A short break restored within staleAfter does not show stale.
func TestFeedShortBreakNotStale(t *testing.T) {
	e := newTestEnv(t)
	e.s.staleAfter = 2 * time.Second
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	epochs := e.s.Stats()["feed_epochs"]
	e.fe.EndEvents()
	for e.s.Stats()["feed_epochs"] == epochs {
		time.Sleep(5 * time.Millisecond)
	}
	e.fe.WaitEventSubscribers(t, 1)
	time.Sleep(100 * time.Millisecond)
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for _, d := range sk.deltas {
		if d.Status != nil && d.Status.State != provider.StatusReady && d.Status.State != provider.StatusLoading {
			t.Fatalf("a short break showed %+v", d.Status)
		}
	}
}

// The events subscription is refused: the view is an error, never an
// empty Ready, even though the list would succeed.
func TestFeedEventsForbidden(t *testing.T) {
	e := newTestEnv(t)
	e.fe.PutContainer(composeContainer(id(1), "p", "web", "running"))
	e.fe.AddHook(enginefake.Fail("/events", 403, "events are not for you"))
	sk := e.watch(KindContainers)
	sk.waitFor(t, "an error", func(_ map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusError && st.Class == provider.ClassForbidden && strings.Contains(st.Message, "not for you")
	})
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for _, d := range sk.deltas {
		if d.Reset || len(d.Upserts) > 0 || d.Status != nil && d.Status.State == provider.StatusReady {
			t.Fatalf("rows or Ready without events: %+v", d)
		}
	}
}

// One inspect hangs: the other rows show, the view is stale with an
// explanation; the object is read again when it changes.
func TestFeedHangingInspect(t *testing.T) {
	e := newTestEnv(t, func(c *engine.Config) { c.RequestTimeout = 300 * time.Millisecond })
	a := composeContainer(id(1), "p", "web", "running")
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(a)
	e.fe.PutContainer(b)
	hang := make(chan struct{})
	e.fe.AddHook(enginefake.StallHeaders("/containers/"+b.ID+"/json", hang))
	sk := e.watch(KindContainers)
	sk.waitFor(t, "stale with a", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		_, hasA := rows[a.ID]
		return hasA && len(rows) == 1 && st.State == provider.StatusStale && strings.Contains(st.Message, "could not be read")
	})
	close(hang)
	e.fe.Emit(containerEvent("update", b))
	sk.waitFor(t, "both, ready", hasRows(a.ID, b.ID))
}

// A delivery the daemon skipped (no break) is repaired only by the user's
// Resync; the rows stay while it reads again.
func TestFeedResyncRepairsSkippedDelivery(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	a.State.Status, a.State.Running = "exited", false
	e.fe.PutContainer(a) // no event: skipped
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(b)
	time.Sleep(50 * time.Millisecond)
	if r, _ := sk.row(a.ID); !strings.Contains(r.Rev, "|running|") {
		t.Fatalf("changed without an event: %q", r.Rev)
	}
	release := make(chan struct{})
	e.fe.AddHook(enginefake.StallHeaders("/containers/json", release))
	if err := e.s.Resync(q); err != nil {
		t.Fatal(err)
	}
	if err := e.s.Resync(q); err != nil { // coalesces
		t.Fatal(err)
	}
	for e.fe.Count("/containers/json") < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	if r, ok := sk.row(a.ID); !ok || !strings.Contains(r.Rev, "|running|") {
		t.Fatalf("rows changed before the new reading: %+v", r)
	}
	e.fe.ClearHooks()
	close(release)
	sk.waitFor(t, "the repaired rows", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusReady && len(rows) == 2 && strings.Contains(rows[a.ID].Rev, "|exited|")
	})
	time.Sleep(100 * time.Millisecond)
	if n := e.fe.Count("/containers/json"); n > 3 {
		t.Fatalf("two Resyncs during one reading listed %d times; want at most one more", n)
	}
	if sk.resets != 1 {
		t.Fatalf("Resync reset the view %d times", sk.resets)
	}
}

// Resync while the reading fails leaves the rows, stale.
func TestFeedFailedResyncKeepsRows(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	q := provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	e.fe.AddHook(enginefake.Fail("/containers/json", 500, "list broke"))
	if err := e.s.Resync(q); err != nil {
		t.Fatal(err)
	}
	sk.waitFor(t, "stale with the rows", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusStale && len(rows) == 1 && strings.Contains(st.Message, "list broke")
	})
	e.fe.ClearHooks()
	sk.waitFor(t, "ready again", hasRows(a.ID))
}

// Past the dirty bound the feed reads everything again (a new epoch)
// instead of queueing.
func TestFeedDirtyOverflow(t *testing.T) {
	e := newTestEnv(t)
	e.s.maxDirty = 10
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the empty snapshot", ready)
	release := make(chan struct{})
	// Hold the inspects so the dirty ids pile up.
	e.fe.AddHook(func(_ http.ResponseWriter, r *http.Request, path string) bool {
		if strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json") && path != "/containers/json" {
			select {
			case <-release:
			case <-r.Context().Done():
				return true
			}
		}
		return false
	})
	first := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(first)
	e.fe.Emit(containerEvent("create", first))
	for e.fe.Count("/containers/"+first.ID+"/json") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	var want []string
	want = append(want, first.ID)
	for i := 2; i <= 40; i++ {
		c := composeContainer(id(i), "p", "web", "running")
		e.fe.PutContainer(c)
		e.fe.Emit(containerEvent("create", c))
		want = append(want, c.ID)
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	sk.waitFor(t, "all 40", hasRows(want...))
	if n := e.s.Stats()["feed_epochs"]; n < 2 {
		t.Fatalf("epochs %d: an overflow must start a new one", n)
	}
}

// A network connect names the container only in its attributes: a known
// container is read again (its networks change) without a container event.
func TestFeedNetworkConnectRereadsContainer(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	a.NetworkSettings.Networks = map[string]engine.EndpointSettings{"extra": {NetworkID: "n1"}}
	e.fe.PutContainer(a)
	e.fe.Emit(engine.Event{Type: "network", Action: "connect", Actor: engine.EventActor{ID: "n1", Attributes: map[string]string{"container": a.ID, "name": "extra"}}})
	sk.waitFor(t, "the new network", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return strings.HasSuffix(rows[a.ID].Rev, "|extra")
	})
	// An unknown container's connect is not read.
	before := e.fe.Count("/containers/" + id(7) + "/json")
	e.fe.Emit(engine.Event{Type: "network", Action: "connect", Actor: engine.EventActor{ID: "n1", Attributes: map[string]string{"container": id(7)}}})
	e.fe.Emit(containerEvent("update", a))
	time.Sleep(50 * time.Millisecond)
	if e.fe.Count("/containers/"+id(7)+"/json") != before {
		t.Fatal("a connect of an unknown container was inspected")
	}
}

// health_status events carry the status after a colon; the subscription's
// "health_status" matches them.
func TestFeedHealthStatusEvent(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	a.Name = "/renamed-by-health" // a visible change
	e.fe.PutContainer(a)
	e.fe.Emit(containerEvent("health_status: unhealthy", a))
	sk.waitFor(t, "the re-read", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return strings.HasPrefix(rows[a.ID].Rev, "/renamed-by-health|")
	})
}

// Rename keeps the row (the key is the id); a new container with the old
// name is another row.
func TestFeedRenameAndReusedName(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	a.Name = "/p-web-1"
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	a.Name = "/old-web"
	e.fe.PutContainer(a)
	e.fe.Emit(containerEvent("rename", a))
	b := composeContainer(id(2), "p", "web", "running")
	b.Name = "/p-web-1"
	e.fe.PutContainer(b)
	e.fe.Emit(containerEvent("create", b))
	sk.waitFor(t, "two rows", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return len(rows) == 2 && strings.HasPrefix(rows[a.ID].Rev, "/old-web|") && strings.HasPrefix(rows[b.ID].Rev, "/p-web-1|")
	})
}

// A feed outlives its last view by the grace, then stops; two views share
// one feed.
func TestFeedLeaseAndGrace(t *testing.T) {
	e := newTestEnv(t)
	e.s.grace = 150 * time.Millisecond
	e.fe.PutContainer(composeContainer(id(1), "p", "web", "running"))
	sk1 := newSink()
	stop1, err := e.s.Watch(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}, sk1)
	if err != nil {
		t.Fatal(err)
	}
	sk2 := newSink()
	stop2, err := e.s.Watch(provider.Query{Kind: KindServices, Scope: core.ScopeSel{Mode: core.ScopeAll}}, sk2)
	if err != nil {
		t.Fatal(err)
	}
	sk1.waitFor(t, "view 1", ready)
	sk2.waitFor(t, "view 2", ready)
	if st := e.s.Stats(); st["feeds_active"] != 1 || st["feed_lists"] != 1 || st["watchers"] != 2 {
		t.Fatalf("two views of one feed: %v", st)
	}
	stop1()
	stop1()
	if st := e.s.Stats(); st["feeds_active"] != 1 {
		t.Fatalf("closing one view stopped the feed: %v", st)
	}
	stop2()
	if st := e.s.Stats(); st["feeds_idle"] != 1 || st["watchers"] != 0 {
		t.Fatalf("after both: %v", st)
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.s.Stats()["feeds_idle"] != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the idle feed was not stopped after the grace")
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.fe.WaitEventSubscribers(t, 0)
	n := len(sk1.deltas)
	e.fe.Emit(containerEvent("start", composeContainer(id(1), "p", "web", "running")))
	time.Sleep(30 * time.Millisecond)
	if len(sk1.deltas) != n {
		t.Fatal("a stopped view got a delivery")
	}
}

// Scopes read the containers, networks and volumes: a project that has
// only a volume is a scope.
func TestFeedScopesFromVolumeOnly(t *testing.T) {
	e := newTestEnv(t)
	e.fe.PutContainer(composeContainer(id(1), "app", "web", "running"))
	e.fe.PutVolume(engine.Volume{Name: "data_vol", CreatedAt: "2026-09-30T00:00:00Z", Labels: map[string]string{LabelProject: "data", LabelVolume: "vol"}})
	sc, err := e.s.Scopes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(sc) != "[{app} {data}]" {
		t.Fatalf("scopes %v", sc)
	}
	if st := e.s.Stats(); st["feeds_idle"] != 3 {
		t.Fatalf("Scopes left feeds %v; want three idle (kept warm)", st)
	}
}

// Get reads the object fresh: a change the feed missed is shown and the
// feed is told to read the object again.
func TestFeedGetFreshAndTouch(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	a.State.Status = "exited"
	e.fe.PutContainer(a) // no event
	res, err := e.s.Get(t.Context(), core.Ref{Provider: ProviderID, Kind: KindContainers, Name: a.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.YAML, `"exited"`) {
		t.Fatalf("Get did not read fresh: %s", res.YAML)
	}
	sk.waitFor(t, "the feed caught up", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return strings.Contains(rows[a.ID].Rev, "|exited|")
	})
	e.fe.RemoveContainer(a.ID)
	if _, err := e.s.Get(t.Context(), core.Ref{Provider: ProviderID, Kind: KindContainers, Name: a.ID}); err == nil {
		t.Fatal("Get of a removed container succeeded")
	}
	sk.waitFor(t, "removed from the view too", hasRows())
}

// Volumes: same-name recreation is a different CreatedAt; the feed holds
// the new one after its events.
func TestFeedVolumeRecreated(t *testing.T) {
	e := newTestEnv(t)
	v := engine.Volume{Name: "p_data", CreatedAt: "2026-09-30T01:00:00Z", Labels: map[string]string{LabelProject: "p"}}
	e.fe.PutVolume(v)
	sk := e.watch(KindVolumes)
	sk.waitFor(t, "the snapshot", hasRows("p_data"))
	v.CreatedAt = "2026-09-30T01:00:05Z"
	e.fe.PutVolume(v)
	e.fe.Emit(engine.Event{Type: "volume", Action: "destroy", Actor: engine.EventActor{ID: "p_data"}})
	e.fe.Emit(engine.Event{Type: "volume", Action: "create", Actor: engine.EventActor{ID: "p_data"}})
	sk.waitFor(t, "the new volume", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return rows["p_data"].Rev == "2026-09-30T01:00:05Z"
	})
}

// Images: a pull names a reference; the tag moves from the old image to
// the new one (untag names the old id).
func TestFeedImageTagMove(t *testing.T) {
	e := newTestEnv(t)
	old := engine.ImageInspect{ID: "sha256:" + strings.Repeat("a", 64), RepoTags: []string{"app:latest"}}
	e.fe.PutImage(old)
	sk := e.watch(KindImages)
	sk.waitFor(t, "the snapshot", hasRows(old.ID))
	nw := engine.ImageInspect{ID: "sha256:" + strings.Repeat("b", 64), RepoTags: []string{"app:latest"}}
	old.RepoTags = nil
	e.fe.PutImage(old)
	e.fe.PutImage(nw)
	e.fe.Emit(engine.Event{Type: "image", Action: "pull", Actor: engine.EventActor{ID: "app:latest"}})
	e.fe.Emit(engine.Event{Type: "image", Action: "untag", Actor: engine.EventActor{ID: old.ID}})
	sk.waitFor(t, "the tag moved", func(rows map[string]core.Row, _ provider.ViewStatus) bool {
		return len(rows) == 2 && rows[old.ID].Rev == "" && rows[nw.ID].Rev == "app:latest"
	})
	e.fe.RemoveImage(old.ID)
	e.fe.Emit(engine.Event{Type: "image", Action: "delete", Actor: engine.EventActor{ID: old.ID}})
	sk.waitFor(t, "the old one deleted", hasRows(nw.ID))
}

// Closing the session ends every feed and its streams.
func TestFeedSessionClose(t *testing.T) {
	e := newTestEnv(t)
	sk := e.watch(KindNetworks)
	sk.waitFor(t, "ready", ready)
	e.fe.WaitEventSubscribers(t, 2)
	e.s.Close()
	e.fe.WaitEventSubscribers(t, 0)
	if _, err := e.s.Watch(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}, newSink()); err == nil {
		t.Fatal("Watch on a closed session succeeded")
	}
}

// An inspect of the old epoch is abandoned when the stream breaks: its
// late answer cannot land over the new epoch's reading.
func TestFeedStaleInspectAcrossReconnect(t *testing.T) {
	e := newTestEnv(t, func(c *engine.Config) { c.RequestTimeout = 5 * time.Second })
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	oldAnswer := append([]byte(nil), mustRaw(t, a)...)
	var mu sync.Mutex
	first := true
	stalled := make(chan struct{})
	e.fe.AddHook(func(w http.ResponseWriter, r *http.Request, path string) bool {
		if path != "/containers/"+a.ID+"/json" {
			return false
		}
		mu.Lock()
		f := first
		first = false
		mu.Unlock()
		if !f {
			return false
		}
		close(stalled)
		<-r.Context().Done() // the client gave up on it
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(oldAnswer)
		return true
	})
	sk := e.watch(KindContainers)
	<-stalled
	a.State.Status = "exited"
	e.fe.PutContainer(a)
	e.fe.EndEvents() // the first epoch ends in the middle of its snapshot
	sk.waitFor(t, "the new epoch's reading", func(rows map[string]core.Row, st provider.ViewStatus) bool {
		return st.State == provider.StatusReady && strings.Contains(rows[a.ID].Rev, "|exited|")
	})
	time.Sleep(50 * time.Millisecond)
	if r, _ := sk.row(a.ID); !strings.Contains(r.Rev, "|exited|") {
		t.Fatalf("the old epoch's answer landed: %q", r.Rev)
	}
}

func mustRaw(t *testing.T, c engine.ContainerInspect) []byte {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// F5 while the feed waits to reconnect: it tries at once, not after the
// backoff.
func TestFeedResyncDuringBackoffRetriesNow(t *testing.T) {
	e := newTestEnv(t)
	e.s.backoff = []time.Duration{time.Hour}
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	infos := e.fe.Count("/info")
	e.fe.EndEvents() // the feed now waits its backoff (an hour) before trying again
	e.fe.WaitEventSubscribers(t, 0)
	time.Sleep(50 * time.Millisecond)
	if n := e.fe.Count("/info"); n != infos {
		t.Fatalf("tried again without waiting: %d", n-infos)
	}
	if err := e.s.Resync(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}); err != nil {
		t.Fatal(err)
	}
	e.fe.WaitEventSubscribers(t, 1) // not an hour later
	sk.waitFor(t, "ready", hasRows(a.ID))
}

// Review P2: the first Ready waits for the reconciliation of what changed
// during the snapshot (a held inspect keeps the view loading).
func TestFeedFirstReadyWaitsForReconciliation(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	b := composeContainer(id(2), "p", "db", "running")
	e.fe.PutContainer(a)
	release := make(chan struct{})
	e.fe.AddHook(enginefake.StallHeaders("/containers/"+b.ID+"/json", release))
	var once sync.Once
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, path string) bool {
		if path != "/containers/json" {
			return false
		}
		done := false
		once.Do(func() {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"Id":%q}]`, a.ID)
			e.fe.PutContainer(b)
			e.fe.Emit(containerEvent("create", b))
			done = true
		})
		return done
	})
	sk := e.watch(KindContainers)
	for e.fe.Count("/containers/"+b.ID+"/json") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	sk.mu.Lock()
	st := sk.status
	sk.mu.Unlock()
	if st.State != provider.StatusLoading {
		t.Fatalf("status %+v while the reconciliation is held", st)
	}
	close(release)
	sk.waitFor(t, "both", hasRows(a.ID, b.ID))
	sk.mu.Lock()
	defer sk.mu.Unlock()
	for _, d := range sk.deltas {
		if d.Status != nil && d.Status.State == provider.StatusReady {
			if _, ok := sk.rows[b.ID]; !ok {
				t.Fatal("Ready without b")
			}
			break
		}
	}
}

// Review P2: a read that succeeds after a failed one recovers the view
// even when the object did not change.
func TestFeedRecoveryWithTheSameBytesIsReady(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	e.fe.AddHook(enginefake.Fail("/containers/"+a.ID+"/json", 500, "hiccup"))
	e.fe.Emit(containerEvent("update", a))
	sk.waitFor(t, "stale", func(_ map[string]core.Row, st provider.ViewStatus) bool { return st.State == provider.StatusStale })
	e.fe.ClearHooks()
	e.fe.Emit(containerEvent("update", a))
	sk.waitFor(t, "ready again", hasRows(a.ID))
}

// Review P2: Get whose fresh read fails says so; it does not show the
// feed's older reading as fresh.
func TestFeedGetFreshReadFailureIsAnError(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	e.fe.AddHook(enginefake.Fail("/containers/"+a.ID+"/json", 403, "no inspecting"))
	_, err := e.s.Get(t.Context(), core.Ref{Provider: ProviderID, Kind: KindContainers, Name: a.ID})
	if !isClass(err, provider.ClassForbidden) {
		t.Fatalf("Get: %v", err)
	}
}

// Review P2: failed reads of objects gone for good do not pile up: past
// the bound the feed reads everything again.
func TestFeedUnresolvedIsBounded(t *testing.T) {
	e := newTestEnv(t)
	e.s.maxUnresolved = 3
	sk := e.watch(KindContainers)
	sk.waitFor(t, "ready", ready)
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, path string) bool {
		if strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json") && path != "/containers/json" {
			http.Error(w, `{"message":"flaky"}`, 500)
			return true
		}
		return false
	})
	for i := 1; i <= 5; i++ {
		c := composeContainer(id(i), "p", "web", "running")
		e.fe.Emit(containerEvent("destroy", c)) // gone: no event will come again
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.s.Stats()["feed_epochs"] < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the unresolved keys piled up without a new reading")
		}
		time.Sleep(5 * time.Millisecond)
	}
	e.fe.ClearHooks()
	sk.waitFor(t, "ready and clean", hasRows())
}

// Review P2: an object that is dirty again after every read does not keep
// a user's Resync waiting.
func TestFeedResyncIsNotStarvedByChurn(t *testing.T) {
	e := newTestEnv(t)
	a := composeContainer(id(1), "p", "web", "running")
	e.fe.PutContainer(a)
	sk := e.watch(KindContainers)
	sk.waitFor(t, "the snapshot", hasRows(a.ID))
	stop := make(chan struct{})
	defer close(stop)
	e.fe.AddHook(func(_ http.ResponseWriter, _ *http.Request, path string) bool {
		if path == "/containers/"+a.ID+"/json" {
			select {
			case <-stop:
			default:
				time.Sleep(5 * time.Millisecond)
				e.fe.Emit(containerEvent("update", a)) // dirty again at once
			}
		}
		return false
	})
	e.fe.Emit(containerEvent("update", a))
	for e.fe.Count("/containers/"+a.ID+"/json") < 5 {
		time.Sleep(2 * time.Millisecond)
	}
	lists := e.fe.Count("/containers/json")
	if err := e.s.Resync(provider.Query{Kind: KindContainers, Scope: core.ScopeSel{Mode: core.ScopeAll}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for e.fe.Count("/containers/json") == lists {
		if time.Now().After(deadline) {
			t.Fatal("Resync starved by churn")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
