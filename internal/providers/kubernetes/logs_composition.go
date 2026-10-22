package kubernetes

import (
	"context"
	"io"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

var _ provider.ComposedLogSource = (*session)(nil)

// A union owns observations directly. It never borrows an overlapping root's
// observation box, whose removal could otherwise terminate a still-selected Pod.
type unionRoot struct {
	ref              core.Ref
	def              *kindDef
	object, pods, rs *unionLease
	failure          provider.LogState
	failingSince     time.Time
	lastKeys         []string
}
type unionLease struct {
	cache *informerCache
	reg   cache.ResourceEventHandlerRegistration
}
type unionTracker struct {
	roots              []unionRoot
	rootOrder          []int
	leases             map[cacheKey]*unionLease
	kick, done, joined chan struct{}
	mu                 sync.Mutex
	members            map[string]*memberPod
	states             []provider.LogState
	synced             bool
	changed            chan struct{}
}

func compositionInvalid() error {
	return &provider.Error{Class: provider.ClassInvalid, Message: "composed logs require 2 to 8 unique UID-pinned Kubernetes Pod or workload roots on one target, with all containers"}
}

// CheckComposedLogRoots validates root UIDs without aggregate inventory,
// inspector YAML, revision history or relation reads.
func (s *session) CheckComposedLogRoots(ctx context.Context, refs []core.Ref) error {
	for _, ref := range refs {
		if _, err := s.getObject(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) StreamComposedLogs(ctx context.Context, refs []core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	if len(refs) < 2 || len(refs) > 8 || q.Channel != provider.ChannelAll || q.Previous || q.Archive && q.Follow {
		return compositionInvalid()
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		def := s.kind(ref.Kind)
		key := ref.Scope + "/" + ref.UID
		if ref.Provider != ProviderID || ref.Target != s.target || ref.Target != refs[0].Target || ref.Scope == "" || ref.Name == "" || ref.UID == "" || def != podsKind && !logWorkloads[def] || seen[key] {
			return compositionInvalid()
		}
		seen[key] = true
	}
	if s.logs == nil {
		return &provider.Error{Class: provider.ClassUnsupported, Message: "logs are not available for this context"}
	}
	tr, err := s.trackUnion(ctx, refs)
	if err != nil {
		return err
	}
	defer tr.stop(s)

	// Filtered live capture and finite history share this exact tracker and one
	// request cap. Reserve four finite-reader slots so a quiet live set cannot
	// starve its initial history scan.
	historyDone := make(chan struct{})
	var historyOnce sync.Once
	open := composedLogOpener(s.logs, provider.NewLogFilter(q.Filter).Enabled() && q.Follow, historyDone)
	fetchMember := func(m memberPod) logFetcher {
		return func(ctx context.Context, req podLogRequest) (io.ReadCloser, error) {
			return open(ctx, req, tr.memberCheck(m))
		}
	}
	run := func(ctx context.Context, query provider.LogQuery, out provider.LogSink) error {
		if !query.Follow {
			defer historyOnce.Do(func() { close(historyDone) })
		}
		out = provider.FilterLogSink(query, out)
		for i, ref := range refs {
			if err := out.Source(i+1, "root/"+ref.Scope+"/"+ref.UID, ref.Scope+"/"+ref.Kind+"/"+ref.Name, ""); err != nil {
				return err
			}
		}
		g := &logGroup{s: s, fetchMember: fetchMember, q: query, sink: &lockedSink{sink: out}, tr: tr, channel: channelsFor(provider.ChannelAll, ""), composed: true, nextID: len(refs)}
		return g.run(ctx)
	}
	return provider.StreamFilteredLogView(ctx, q, run, sink)
}

func (s *session) trackUnion(ctx context.Context, refs []core.Ref) (*unionTracker, error) {
	ctx, cancel := context.WithTimeout(ctx, groupSyncTimeout)
	defer cancel()
	t := &unionTracker{leases: map[cacheKey]*unionLease{}, kick: make(chan struct{}, 1), done: make(chan struct{}), joined: make(chan struct{}), members: map[string]*memberPod{}, changed: make(chan struct{}), states: make([]provider.LogState, len(refs))}
	// Workloads need namespace inventory. Explicit Pods retain a name-narrowed
	// cache even beside a workload: resourceNames RBAC may allow the Pod while
	// denying that workload's whole-namespace LIST.
	for _, ref := range refs {
		r := unionRoot{ref: ref, def: s.kind(ref.Kind)}
		if _, err := s.getObject(ctx, ref); err != nil {
			class, msg := classify(err)
			r.failure = provider.LogState{State: provider.LogError, Class: class, Message: boundedRootLogMessage(msg)}
		}
		t.roots = append(t.roots, r)
	}
	// Namespace membership is the observation authority while it selects a
	// Pod. Named roots remain independent for RBAC, but cannot overwrite that
	// observation with a lagging cache in the same recomputation. RVs are opaque.
	for _, podRoots := range []bool{false, true} {
		for i, r := range t.roots {
			if (r.def == podsKind) == podRoots {
				t.rootOrder = append(t.rootOrder, i)
			}
		}
	}
	lease := func(key cacheKey, def *kindDef) (*unionLease, error) {
		if l := t.leases[key]; l != nil {
			return l, nil
		}
		c, ok := s.caches.acquire(key, def)
		if !ok {
			return nil, errSessionClosed
		}
		l := &unionLease{cache: c}
		t.leases[key] = l
		h := cache.ResourceEventHandlerFuncs{AddFunc: func(any) { t.poke() }, UpdateFunc: func(any, any) { t.poke() }, DeleteFunc: func(any) { t.poke() }}
		reg, err := c.inf.AddEventHandler(h)
		l.reg = reg
		return l, err
	}
	fail := func(err error) (*unionTracker, error) { t.release(s); return nil, err }
	for i := range t.roots {
		r := &t.roots[i]
		if r.failure.State != "" {
			continue
		}
		var err error
		r.object, err = lease(cacheKey{gvr: r.def.gvr, namespace: r.ref.Scope, selector: "metadata.name=" + r.ref.Name}, r.def)
		if err != nil {
			return fail(err)
		}
		key := cacheKey{gvr: podsKind.gvr, namespace: r.ref.Scope}
		if r.def == podsKind {
			key.selector = "metadata.name=" + r.ref.Name
		}
		r.pods, err = lease(key, podsKind)
		if err != nil {
			return fail(err)
		}
		if r.def == deploymentsKind {
			r.rs, err = lease(cacheKey{gvr: replicaSetsKind.gvr, namespace: r.ref.Scope}, replicaSetsKind)
			if err != nil {
				return fail(err)
			}
		}
	}
	go t.loop()
	return t, nil
}
func (t *unionTracker) poke() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}
func (t *unionTracker) release(s *session) {
	for _, l := range t.leases {
		if l.reg != nil {
			_ = l.cache.inf.RemoveEventHandler(l.reg)
		}
		s.caches.release(l.cache)
	}
}
func (t *unionTracker) stop(s *session) { close(t.done); <-t.joined; t.release(s) }
func (t *unionTracker) loop() {
	defer close(t.joined)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-t.kick:
		case <-tick.C:
			if !t.needsTick() {
				continue
			}
		}
		t.recompute()
	}
}

// Healthy synchronized caches change membership through event kicks. Ticks
// only advance initialization and transport uncertainty, never rescan a quiet
// namespace every 100 ms.
func (t *unionTracker) needsTick() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.synced {
		return true
	}
	for _, r := range t.roots {
		if !r.failingSince.IsZero() {
			return true
		}
	}
	for _, l := range t.leases {
		if l.cache.transport().failing {
			return true
		}
	}
	return false
}
func (t *unionTracker) recompute() {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := map[string]bool{}
	uncertain := map[string]string{}
	changed := false
	settled := true
	now := time.Now()
	conflicts := t.identityConflicts()
	for _, i := range t.rootOrder {
		r := &t.roots[i]
		state := r.failure
		var selected []metav1.Object
		membershipUnknown, malformed := false, false
		if state.State == "" {
			cs := []*informerCache{r.object.cache, r.pods.cache}
			ready := r.object.reg.HasSynced() && r.pods.reg.HasSynced()
			if r.rs != nil {
				cs = append(cs, r.rs.cache)
				ready = ready && r.rs.reg.HasSynced()
			}
			blind := cacheProblem(now, &r.failingSince, cs...)
			switch {
			case blind != "":
				membershipUnknown = true
				state = provider.LogState{State: provider.LogError, Class: provider.ClassUnavailable, Message: blind}
			case !ready:
				membershipUnknown = true
				settled = false
				state = provider.LogState{State: provider.LogWaiting, Message: "waiting for source membership"}
			default:
				obj, exists, _ := r.object.cache.inf.GetStore().GetByKey(r.ref.Scope + "/" + r.ref.Name)
				o, ok := obj.(metav1.Object)
				if !exists || !ok || string(o.GetUID()) != r.ref.UID {
					state = provider.LogState{State: provider.LogError, Class: provider.ClassGone, Message: "the selected root was deleted or replaced"}
				} else {
					owners := map[types.UID]bool{types.UID(r.ref.UID): true}
					if r.rs != nil {
						owners = map[types.UID]bool{}
						for _, obj := range r.rs.cache.inf.GetStore().List() {
							if o, ok := obj.(metav1.Object); ok && controllerUID(o) == types.UID(r.ref.UID) {
								owners[o.GetUID()] = true
							}
						}
					}
					for _, obj := range r.pods.cache.inf.GetStore().List() {
						p, ok := obj.(metav1.Object)
						if !ok || p.GetNamespace() != r.ref.Scope {
							continue
						}
						if r.def == podsKind {
							if p.GetName() != r.ref.Name || string(p.GetUID()) != r.ref.UID {
								continue
							}
						} else if !owners[controllerUID(p)] {
							continue
						}
						if p.GetUID() == "" || p.GetName() == "" {
							malformed = true
							continue
						}
						selected = append(selected, p)
					}
					state = provider.LogState{State: provider.LogStreaming}
					if len(selected) == 0 {
						state = provider.LogState{State: provider.LogWaiting, Message: "no matching Pods are currently observed"}
					}
				}
			}
		}

		// A broken watch is unknown coverage, not evidence that a Pod vanished.
		// Preserve its observation until a healthy overlapping root or a new sync
		// supplies authoritative membership again.

		if malformed {
			state = provider.LogState{State: provider.LogError, Class: provider.ClassUnavailable, Message: "some selected Pod identities are unavailable"}
		}
		for _, o := range selected {
			if conflicts[o.GetNamespace()+"/"+o.GetName()] {
				state = provider.LogState{State: provider.LogError, Class: provider.ClassUnavailable, Message: compositionIdentityConflict}
				break
			}
		}
		state.Message = boundedRootLogMessage(state.Message)
		unknown := r.failure.State == "" && membershipUnknown
		if unknown {
			for _, key := range r.lastKeys {
				uncertain[key] = state.Message
			}
		} else {
			r.lastKeys = nil
		}
		if state != t.states[i] {
			t.states[i] = state
			changed = true
		}
		for _, o := range selected {
			key := o.GetNamespace() + "/" + string(o.GetUID())
			r.lastKeys = append(r.lastKeys, key)
			if seen[key] {
				continue
			}
			seen[key] = true
			m := t.members[key]
			conflict := conflicts[o.GetNamespace()+"/"+o.GetName()]
			if m != nil && !m.conflicted && !conflict && m.rv == o.GetResourceVersion() {
				m.box.setBlind("")
				continue
			}
			u, ok := asUnstructured(o)
			if !ok {
				continue
			}
			if m == nil {
				m = &memberPod{uid: string(o.GetUID()), namespace: o.GetNamespace(), name: o.GetName(), created: o.GetCreationTimestamp().Time, box: newObsBox()}
				t.members[key] = m
				changed = true
			}

			if conflict {
				if !m.conflicted || len(m.ctrs) > 0 {
					changed = true
				}
				m.conflicted = true
				m.ctrs = nil
				m.box.setBlind(compositionIdentityConflict)
				m.box.setSynced()
				continue
			}
			if m.conflicted {
				m.conflicted = false
				changed = true
			}
			m.rv = o.GetResourceVersion()
			ctrs := ctrOrder(u.Object)
			if !slices.Equal(ctrs, m.ctrs) {
				m.ctrs = ctrs
				changed = true
			}
			m.box.set(observePod(u))
			m.box.setBlind("")
			m.box.setSynced()
		}
	}
	for key, m := range t.members {
		if reason, ok := uncertain[key]; ok && !seen[key] {
			m.box.setBlind(reason)
			continue
		}
		if !seen[key] {
			m.box.setBlind("")
			m.box.setSynced()
			m.box.set(podObs{})
			delete(t.members, key)
			changed = true
		}
	}
	if t.synced != settled {
		t.synced = settled
		changed = true
	}
	if changed {
		close(t.changed)
		t.changed = make(chan struct{})
	}
}
func (t *unionTracker) snapshot() ([]memberPod, bool, string, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []memberPod
	for _, m := range t.members {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].created.Equal(out[j].created) {
			return out[i].created.After(out[j].created)
		}
		return out[i].namespace+"/"+out[i].name+"/"+out[i].uid < out[j].namespace+"/"+out[j].name+"/"+out[j].uid
	})
	return out, t.synced, "", t.changed
}
func (t *unionTracker) coverage() []provider.LogState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.states)
}

func composedLogFetcher(fetch logFetcher, filtered bool, historyDone <-chan struct{}) logFetcher {
	open := composedLogOpener(fetch, filtered, historyDone)
	return func(ctx context.Context, req podLogRequest) (io.ReadCloser, error) { return open(ctx, req, nil) }
}
func composedLogOpener(fetch logFetcher, filtered bool, historyDone <-chan struct{}) func(context.Context, podLogRequest, func() error) (io.ReadCloser, error) {
	all := make(chan struct{}, maxGroupStreams)
	live := make(chan struct{}, maxGroupStreams-backlogWorkers)
	return func(ctx context.Context, req podLogRequest, before func() error) (io.ReadCloser, error) {
		liveTaken := filtered && req.Follow
		if liveTaken {
			select {
			case live <- struct{}{}:
			case <-historyDone:
				liveTaken = false
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		select {
		case all <- struct{}{}:
		case <-ctx.Done():
			if liveTaken {
				<-live
			}
			return nil, ctx.Err()
		}
		release := func() {
			<-all
			if liveTaken {
				<-live
			}
		}

		if before != nil {
			if err := before(); err != nil {
				release()
				return nil, err
			}
		}
		rc, err := fetch(ctx, req)
		if err != nil {
			release()
			return nil, err
		}
		return &compositionBody{ReadCloser: rc, release: release}, nil
	}
}

// Release admission only after closing the actual response, once even when a
// cancellation races a normal reader exit.
type compositionBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
	err     error
}

func (b *compositionBody) Close() error {
	b.once.Do(func() { b.err = b.ReadCloser.Close(); b.release() })
	return b.err
}

func boundedRootLogMessage(message string) string {
	const limit = 4096
	if len(message) <= limit {
		return message
	}
	message = message[:limit-3]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message + "..."
}

const compositionIdentityConflict = "Pod identity observations disagree; waiting for caches to agree"

// Cache resource versions are opaque. A UID disagreement (including a known
// absence) is unknown identity, never permission to read a replacement by name.
func (t *unionTracker) podIdentityIssue(ns, name, uid string) string {
	observed := false
	for key, l := range t.leases {
		if key.gvr != podsKind.gvr || key.namespace != ns || key.selector != "" && key.selector != "metadata.name="+name || !l.reg.HasSynced() || l.cache.transport().failing {
			continue
		}
		observed = true
		obj, exists, _ := l.cache.inf.GetStore().GetByKey(ns + "/" + name)
		o, ok := obj.(metav1.Object)
		if !exists || !ok {
			if uid != "" {
				return compositionIdentityConflict
			}
			continue
		}
		if string(o.GetUID()) != uid {
			return compositionIdentityConflict
		}
	}
	if !observed {
		return "Pod observations are unavailable"
	}
	return ""
}
func (t *unionTracker) identityConflicts() map[string]bool {
	conflicts := map[string]bool{}
	for key, l := range t.leases {
		if key.gvr != podsKind.gvr || key.selector == "" || !l.reg.HasSynced() || l.cache.transport().failing {
			continue
		}
		name := strings.TrimPrefix(key.selector, "metadata.name=")
		obj, exists, _ := l.cache.inf.GetStore().GetByKey(key.namespace + "/" + name)
		uid := ""
		if o, ok := obj.(metav1.Object); exists && ok {
			uid = string(o.GetUID())
		}
		if t.podIdentityIssue(key.namespace, name, uid) != "" {
			conflicts[key.namespace+"/"+name] = true
		}
	}
	return conflicts
}
func (t *unionTracker) memberCheck(m memberPod) func() error {
	return func() error {
		obs, synced, blind, _ := m.box.get()
		if blind != "" {
			return &provider.Error{Class: provider.ClassUnavailable, Message: boundedRootLogMessage(blind)}
		}
		if !synced {
			return &provider.Error{Class: provider.ClassUnavailable, Message: "Pod observations are unavailable"}
		}
		if !obs.exists || obs.uid != m.uid {
			return errMemberGone
		}
		if reason := t.podIdentityIssue(m.namespace, m.name, m.uid); reason != "" {
			return &provider.Error{Class: provider.ClassUnavailable, Message: reason}
		}
		return nil
	}
}
