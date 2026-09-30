package compose

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

var _ provider.MetricsSource = (*session)(nil)

// Usage of containers and services from the Engine's stats.
//
// Every sample is the daemon's own two-point one (stats without one-shot:
// it reads the cgroup twice ~1 s apart), so CPU is the rate over that
// second of that container incarnation — never a delta against an older
// sample of ours that a restart unseen by the feed, or a long pause of the
// page, would make wrong (Codex plan review #9). The daemon's wait is
// spent in parallel: statsPool requests at once per session.
const (
	statsPool = 32
	// maxServiceSum: a service's sum is over at most this many running
	// members (more: the sum is partial).
	maxServiceSum = 20
)

// Metrics: CPU (cores) and memory (bytes, without the inactive file cache)
// of the running containers among rowIDs, or of the running members of the
// services among them (a sum, partial where a member has no value).
// Stopped containers have no usage (absent). Linux daemons only.
func (s *session) Metrics(ctx context.Context, q provider.Query, rowIDs []string) (provider.Metrics, error) {
	if q.Kind != KindContainers && q.Kind != KindServices {
		return provider.Metrics{}, &provider.Error{Class: provider.ClassUnsupported, Message: "no usage for " + q.Kind}
	}
	if err := s.linuxDaemon(ctx); err != nil {
		return provider.Metrics{}, err
	}
	fs, err := s.acquire([]Feed{FeedContainers})
	if err != nil {
		return provider.Metrics{}, err
	}
	defer s.release(fs)
	wctx, cancel := context.WithTimeout(ctx, waitTimeout)
	err = waitAll(wctx, fs)
	cancel()
	if err != nil {
		return provider.Metrics{}, err
	}
	objs, _, _, _ := fs[0].observe()

	// the containers of each row
	parts := map[string][]string{}
	cut := map[string]bool{}
	var ids []string
	seen := map[string]bool{}
	add := func(row string, c *engine.ContainerInspect) {
		parts[row] = append(parts[row], c.ID)
		if !seen[c.ID] {
			seen[c.ID] = true
			ids = append(ids, c.ID)
		}
	}
	for _, row := range rowIDs {
		switch q.Kind {
		case KindContainers:
			if o, ok := objs[row]; ok {
				if c := o.(*engine.ContainerInspect); c.State.Running {
					add(row, c)
				}
			}
		case KindServices:
			project, service, ok := strings.Cut(row, "/")
			if !ok {
				continue
			}
			n := 0
			for _, c := range membersOf(objs, project, service) {
				if !c.State.Running {
					continue
				}
				if n == maxServiceSum {
					cut[row] = true
					break
				}
				n++
				add(row, c)
			}
			if n == 0 {
				parts[row] = nil // nothing runs: no usage, not unknown
			}
		}
	}

	samples := s.sampleAll(ctx, ids)
	out := provider.Metrics{Values: map[string]provider.Usage{}}
	var window time.Duration
	for row, cs := range parts {
		var u provider.Usage
		var cpu, mem float64
		haveCPU, haveMem := 0, 0
		for _, id := range cs {
			sm, ok := samples[id]
			if !ok {
				continue
			}
			if sm.cpu != nil {
				cpu += *sm.cpu
				haveCPU++
			}
			if sm.mem != nil {
				mem += *sm.mem
				haveMem++
			}
			if sm.read.After(u.At) {
				u.At = sm.read
			}
			if window == 0 && sm.window > 0 {
				window = sm.window
			}
		}
		if haveCPU > 0 {
			u.CPU = provider.Num(cpu)
			u.CPUPartial = haveCPU < len(cs) || cut[row]
		}
		if haveMem > 0 {
			u.Memory = provider.Num(mem)
			u.MemoryPartial = haveMem < len(cs) || cut[row]
		}
		if u.CPU == nil && u.Memory == nil {
			continue // unknown (or nothing runs)
		}
		out.Values[row] = u
		if u.At.After(out.Timestamp) {
			out.Timestamp = u.At
		}
	}
	if window > 0 {
		out.Window = window.Round(100 * time.Millisecond).String()
	}
	return out, nil
}

// linuxDaemon: usage is read from Linux cgroups; a Windows daemon's stats
// have other fields. Asked once per session (a failed ask is not kept).
func (s *session) linuxDaemon(ctx context.Context) error {
	s.mu.Lock()
	osType := s.osType
	s.mu.Unlock()
	if osType == "" {
		info, err := s.cl.Info(ctx)
		if err != nil {
			return providerError(err)
		}
		osType = info.OSType
		s.mu.Lock()
		s.osType = osType
		s.mu.Unlock()
	}
	if osType != "linux" {
		return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("container usage is shown for Linux Docker Engines only (this one is %q)", osType)}
	}
	return nil
}

type sample struct {
	cpu, mem *float64
	read     time.Time
	window   time.Duration
}

// sampleAll reads the containers' stats, statsPool at a time; a failed or
// empty read has no entry. It returns once all are read or ctx ends.
func (s *session) sampleAll(ctx context.Context, ids []string) map[string]sample {
	out := make(map[string]sample, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	pool := make(chan struct{}, statsPool)
	for _, id := range ids {
		select {
		case pool <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-pool }()
			st, err := s.cl.ContainerStats(ctx, id, false)
			if err != nil {
				return
			}
			if sm, ok := usageOf(st); ok {
				mu.Lock()
				out[id] = sm
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return out
}

// usageOf reads a two-point sample: CPU in cores from the deltas (a
// negative CPU delta or no system delta — unknown), memory without the
// inactive file cache like `docker stats` (the user's SPK-launcher
// effectiveMemoryUsage: inactive_file on cgroup v2, total_inactive_file on
// v1, cache as a coarse fallback; none — the raw usage). A zero read time:
// the container is not running.
func usageOf(st engine.Stats) (sample, bool) {
	if st.Read.IsZero() {
		return sample{}, false
	}
	sm := sample{read: st.Read}
	if !st.PreRead.IsZero() && st.Read.After(st.PreRead) {
		sm.window = st.Read.Sub(st.PreRead)
	}
	cpuDelta := st.CPUStats.CPUUsage.TotalUsage - st.PreCPUStats.CPUUsage.TotalUsage
	sysDelta := st.CPUStats.SystemCPUUsage - st.PreCPUStats.SystemCPUUsage
	online := st.CPUStats.OnlineCPUs
	if online == 0 {
		online = len(st.CPUStats.CPUUsage.PercpuUsage)
	}
	if st.PreCPUStats.SystemCPUUsage > 0 && sysDelta > 0 && cpuDelta >= 0 && online > 0 {
		sm.cpu = provider.Num(float64(cpuDelta) / float64(sysDelta) * float64(online))
	}
	if m := st.MemoryStats; m.Usage > 0 {
		used := m.Usage
		for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
			if v, ok := m.Stats[key]; ok && v > 0 {
				used = max(m.Usage-v, 0)
				break
			}
		}
		sm.mem = provider.Num(float64(used))
	}
	return sm, sm.cpu != nil || sm.mem != nil
}
