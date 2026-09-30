package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

func metricsQuery(kind string) provider.Query {
	return provider.Query{Kind: kind, Scope: core.ScopeSel{Mode: core.ScopeAll}}
}

// statsJSON is a two-point sample: cpu/pre total and system counters,
// memory usage and its stats.
func statsJSON(total, preTotal, system, preSystem int64, online int, usage int64, memStats map[string]int64) []byte {
	now := time.Now().UTC()
	b, _ := json.Marshal(map[string]any{
		"read": now.Format(time.RFC3339Nano), "preread": now.Add(-time.Second).Format(time.RFC3339Nano),
		"cpu_stats":    map[string]any{"cpu_usage": map[string]any{"total_usage": total}, "system_cpu_usage": system, "online_cpus": online},
		"precpu_stats": map[string]any{"cpu_usage": map[string]any{"total_usage": preTotal}, "system_cpu_usage": preSystem, "online_cpus": online},
		"memory_stats": map[string]any{"usage": usage, "stats": memStats},
	})
	return b
}

// Containers: CPU in cores from the daemon's two points, memory without
// the inactive file cache; a stopped one and a row not asked have nothing.
func TestMetricsOfContainers(t *testing.T) {
	e := newTestEnv(t)
	up := replica("aaaa1111", "p", "web", "1", "running")
	down := replica("bbbb2222", "p", "web", "2", "exited")
	other := replica("cccc3333", "p", "db", "1", "running")
	for _, c := range []engine.ContainerInspect{up, down, other} {
		e.fe.PutContainer(c)
	}
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{up.ID, down.ID, "nope"})
	require.NoError(t, err)
	require.Len(t, m.Values, 1)
	u := m.Values[up.ID]
	require.NotNil(t, u.CPU)
	require.NotNil(t, u.Memory)
	assert.InDelta(t, 0.5, *u.CPU, 1e-9, "0.5 s of CPU per 2 CPU-seconds of 2 CPUs")
	assert.InDelta(t, 48<<20, *u.Memory, 0, "64 MiB less 16 MiB inactive file cache")
	assert.False(t, u.CPUPartial || u.MemoryPartial)
	assert.False(t, u.At.IsZero())
	assert.Equal(t, "1s", m.Window)
	assert.Zero(t, e.fe.Count("/containers/"+other.ID+"/stats"), "only the rows asked for")
	assert.Zero(t, e.fe.Count("/containers/"+down.ID+"/stats"), "a stopped container is not asked")
	last := e.fe.Requests()
	for _, r := range last {
		if strings.HasSuffix(r.Path, "/stats") {
			assert.Empty(t, r.Query.Get("one-shot"), "the daemon's two-point sample")
		}
	}
}

// A service sums its running members; a member without a CPU value makes
// the CPU partial (not a false 0) while the memory stays whole.
func TestMetricsOfServicesArePartialPerMetric(t *testing.T) {
	e := newTestEnv(t)
	one := replica("aaaa1111", "p", "web", "1", "running")
	two := replica("bbbb2222", "p", "web", "2", "running")
	stopped := replica("cccc3333", "p", "web", "3", "exited")
	idle := replica("dddd4444", "p", "idle", "1", "exited")
	for _, c := range []engine.ContainerInspect{one, two, stopped, idle} {
		e.fe.PutContainer(c)
	}
	e.fe.SetStats(func(id string, _ bool) []byte {
		if id == two.ID {
			// no system delta: CPU unknown, memory known
			return statsJSON(9e8, 8e8, 4e9, 4e9, 2, 10<<20, map[string]int64{"inactive_file": 0})
		}
		return statsJSON(15e8, 5e8, 4e9, 2e9, 2, 64<<20, map[string]int64{"inactive_file": 16 << 20})
	})
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindServices), []string{"p/web", "p/idle"})
	require.NoError(t, err)
	u, ok := m.Values["p/web"]
	require.True(t, ok)
	require.NotNil(t, u.CPU)
	assert.InDelta(t, 1.0, *u.CPU, 1e-9)
	assert.True(t, u.CPUPartial, "one member's CPU is unknown")
	require.NotNil(t, u.Memory)
	assert.InDelta(t, 48<<20+10<<20, *u.Memory, 0)
	assert.False(t, u.MemoryPartial, "every running member's memory is known")
	_, ok = m.Values["p/idle"]
	assert.False(t, ok, "nothing runs: no usage")
}

// A member whose stats fail leaves both metrics partial; all failing —
// the service has no value (unknown), never zero.
func TestMetricsOfAServiceWithFailingMembers(t *testing.T) {
	e := newTestEnv(t)
	one := replica("aaaa1111", "p", "web", "1", "running")
	two := replica("bbbb2222", "p", "web", "2", "running")
	e.fe.PutContainer(one)
	e.fe.PutContainer(two)
	failing := map[string]bool{"/containers/" + two.ID + "/stats": true}
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if !failing[p] {
			return false
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"cgroup gone"}`)
		return true
	})
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindServices), []string{"p/web"})
	require.NoError(t, err)
	u := m.Values["p/web"]
	require.NotNil(t, u.CPU)
	assert.InDelta(t, 0.5, *u.CPU, 1e-9)
	assert.True(t, u.CPUPartial && u.MemoryPartial)

	failing["/containers/"+one.ID+"/stats"] = true
	_, err = e.s.Metrics(t.Context(), metricsQuery(KindServices), []string{"p/web"})
	assert.Equal(t, provider.ClassUnavailable, errClass(err), "every read failed: an error, not an empty success")
}

// Every read refused: the refusal is the answer (not "ok" with nothing).
func TestMetricsAllRefused(t *testing.T) {
	e := newTestEnv(t)
	one := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(one)
	e.fe.AddHook(func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasSuffix(p, "/stats") {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"authorization denied by plugin"}`)
		return true
	})
	_, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{one.ID})
	assert.Equal(t, provider.ClassForbidden, errClass(err))
	assert.Contains(t, err.Error(), "authorization denied")
}

// A restart between the daemon's two points leaves the CPU unknown even
// when the new incarnation's counter is the larger (the fresh inspect
// proves the incarnation began before the first point); memory, a value
// at the second point, stays.
func TestMetricsCPUOnlyWithinOneIncarnation(t *testing.T) {
	e := newTestEnv(t)
	c := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(c)
	e.fe.SetStats(func(string, bool) []byte {
		b := statsJSON(9e8, 1e8, 4e9, 2e9, 2, 64<<20, map[string]int64{"inactive_file": 16 << 20})
		restarted := c
		restarted.State.StartedAt = engine.TimeOf(time.Now().UTC().Add(-500 * time.Millisecond)) // after preread
		e.fe.PutContainer(restarted)
		return b
	})
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{c.ID})
	require.NoError(t, err)
	u := m.Values[c.ID]
	assert.Nil(t, u.CPU, "the counters are of two incarnations")
	require.NotNil(t, u.Memory)
	assert.InDelta(t, 48<<20, *u.Memory, 0)
}

// Many services, slow stats, a budget for one batch of statsPool reads:
// the first member of every row is read before any row's second, so every
// shown row gets a value (in the rows' order, the batch would be the first
// rows' members only).
func TestMetricsCoverEveryRowFirst(t *testing.T) {
	e := newTestEnv(t)
	var rows []string
	for r := range 12 {
		svc := fmt.Sprintf("s%02d", r)
		rows = append(rows, "p/"+svc)
		for n := 1; n <= 5; n++ {
			e.fe.PutContainer(replica(fmt.Sprintf("%s%06d", svc, n), "p", svc, fmt.Sprint(n), "running"))
		}
	}
	e.fe.AddHook(func(_ http.ResponseWriter, r *http.Request, p string) bool {
		if strings.HasSuffix(p, "/stats") {
			select {
			case <-time.After(300 * time.Millisecond):
			case <-r.Context().Done():
			}
		}
		return false
	})
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	m, err := e.s.Metrics(ctx, metricsQuery(KindServices), rows)
	require.NoError(t, err, "some were read: values, not an error")
	for _, r := range rows {
		_, ok := m.Values[r]
		assert.True(t, ok, r)
	}
}

// The stats slots are the session's: two views at once still read at
// most statsPool at a time.
func TestMetricsShareTheSessionsSlots(t *testing.T) {
	e := newTestEnv(t)
	e.s.statsSlots = make(chan struct{}, 3)
	var ids []string
	for n := range 8 {
		c := replica(fmt.Sprintf("c%07d", n), "p", "web", fmt.Sprint(n+1), "running")
		e.fe.PutContainer(c)
		ids = append(ids, c.ID)
	}
	var mu sync.Mutex
	active, most := 0, 0
	e.fe.AddHook(func(_ http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasSuffix(p, "/stats") {
			return false
		}
		mu.Lock()
		active++
		most = max(most, active)
		mu.Unlock()
		time.Sleep(30 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return false
	})
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), ids)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, most, 3)
	assert.Equal(t, 3, most, "the slots are used")
}

// Past maxServiceSum running members the sum is partial and the rest are
// not asked.
func TestMetricsOfALargeServiceAreCut(t *testing.T) {
	e := newTestEnv(t)
	for i := 1; i <= maxServiceSum+5; i++ {
		e.fe.PutContainer(replica(fmt.Sprintf("%08d", i), "p", "web", fmt.Sprint(i), "running"))
	}
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindServices), []string{"p/web"})
	require.NoError(t, err)
	u := m.Values["p/web"]
	require.NotNil(t, u.CPU)
	assert.InDelta(t, 0.5*maxServiceSum, *u.CPU, 1e-6)
	assert.True(t, u.CPUPartial && u.MemoryPartial)
	n := 0
	for _, r := range e.fe.Requests() {
		if strings.HasSuffix(r.Path, "/stats") {
			n++
		}
	}
	assert.Equal(t, maxServiceSum, n)
}

// cgroup v1 counters, a missing cache counter (raw usage), and a counter
// that went back (a restart inside the daemon's second): unknown CPU.
func TestUsageOfSamples(t *testing.T) {
	st := func(b []byte) engine.Stats {
		var s engine.Stats
		require.NoError(t, json.Unmarshal(b, &s))
		return s
	}
	sm, ok := usageOf(st(statsJSON(3e8, 1e8, 3e9, 1e9, 4, 100, map[string]int64{"total_inactive_file": 30, "inactive_file": 10, "cache": 40})))
	require.True(t, ok)
	assert.InDelta(t, 0.4, *sm.cpu, 1e-9)
	assert.InDelta(t, 70, *sm.mem, 0, "cgroup v1 (both counters): the hierarchy's total_inactive_file, as docker stats")
	for _, x := range []struct {
		stats map[string]int64
		want  float64
		why   string
	}{
		{map[string]int64{"inactive_file": 10}, 90, "cgroup v2: inactive_file"},
		{map[string]int64{"total_inactive_file": 0, "inactive_file": 0, "cache": 30}, 100, "a zero counter is a value, not a missing one"},
		{map[string]int64{"inactive_file": 0, "cache": 30}, 100, "v2 zero: the cache is not a fallback"},
		{map[string]int64{"inactive_file": 150}, 100, "a counter above the usage: the raw usage, as docker stats"},
		{nil, 100, "no counter: the raw usage"},
	} {
		sm, ok = usageOf(st(statsJSON(3e8, 1e8, 3e9, 1e9, 4, 100, x.stats)))
		require.True(t, ok)
		assert.InDelta(t, x.want, *sm.mem, 0, x.why)
	}
	sm, ok = usageOf(st(statsJSON(1e8, 3e8, 3e9, 1e9, 4, 100, nil)))
	require.True(t, ok)
	assert.Nil(t, sm.cpu, "the CPU counter went back: unknown")
	sm, ok = usageOf(st(statsJSON(1e8, 0, 3e9, 0, 4, 100, nil)))
	require.True(t, ok)
	assert.Nil(t, sm.cpu, "no previous point (one-shot): unknown")
	_, ok = usageOf(engine.Stats{})
	assert.False(t, ok, "not running")
}

func TestMetricsOnlyForLinuxContainersAndServices(t *testing.T) {
	e := newTestEnv(t)
	_, err := e.s.Metrics(t.Context(), metricsQuery(KindNetworks), nil)
	assert.Equal(t, provider.ClassUnsupported, errClass(err))
	info := engine.Info{ID: "W", OSType: "windows"}
	e.fe.SetInfo(info)
	_, err = e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{"x"})
	assert.Equal(t, provider.ClassUnsupported, errClass(err))
	assert.Contains(t, err.Error(), "windows")
}

// The page leaving ends the sampling promptly.
func TestMetricsEndWithTheCaller(t *testing.T) {
	e := newTestEnv(t, func(c *engine.Config) { c.RequestTimeout = 30 * time.Second })
	web := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(web)
	release := make(chan struct{})
	defer close(release)
	e.fe.AddHook(func(_ http.ResponseWriter, _ *http.Request, p string) bool {
		if !strings.HasSuffix(p, "/stats") {
			return false
		}
		<-release
		return true
	})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)
	t0 := time.Now()
	_, err := e.s.Metrics(ctx, metricsQuery(KindContainers), []string{web.ID})
	assert.ErrorIs(t, err, context.Canceled, "nothing read: the caller's end, not an empty success")
	assert.Less(t, time.Since(t0), 2*time.Second)
}

// A caller that gave up gives its slots back, whichever way the wait ended.
func TestMetricsGiveTheSlotsBack(t *testing.T) {
	e := newTestEnv(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 200 {
		_, err := e.s.sampleAll(ctx, []string{"a", "b", "c"})
		assert.ErrorIs(t, err, context.Canceled)
	}
	assert.Zero(t, len(e.s.statsSlots), "no slot kept")
	c := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(c)
	m, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{c.ID})
	require.NoError(t, err)
	assert.NotNil(t, m.Values[c.ID].CPU)
}

// The inspect proving the incarnation fails: with no value left the
// failure is the answer, not an empty success.
func TestMetricsInspectFailureIsSaid(t *testing.T) {
	e := newTestEnv(t)
	c := replica("aaaa1111", "p", "web", "1", "running")
	e.fe.PutContainer(c)
	_, err := e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{c.ID}) // the feed is read
	require.NoError(t, err)
	e.fe.SetStats(func(string, bool) []byte { return statsJSON(9e8, 1e8, 4e9, 2e9, 2, 0, nil) }) // CPU only
	e.fe.AddHook(func(w http.ResponseWriter, r *http.Request, p string) bool {
		if r.Method != http.MethodGet || p != "/containers/"+c.ID+"/json" {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"denied by plugin"}`)
		return true
	})
	_, err = e.s.Metrics(t.Context(), metricsQuery(KindContainers), []string{c.ID})
	assert.Equal(t, provider.ClassForbidden, errClass(err))
}
