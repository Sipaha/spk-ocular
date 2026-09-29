package kubernetes

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
)

// memberPod is one pod of a log group with its observation box.
type memberPod struct {
	uid, name string
	created   time.Time
	rv        string
	ctrs      []string // container names in kubectl order
	box       *obsBox
}

// groupTracker keeps the live set of a group's pods (a workload's, by
// controller UID — Deployment → ReplicaSet → Pod — or one pod's) from the
// informer caches, and feeds each member's observation box. Informer
// handlers only kick it (non-blocking); the work is on its own goroutine.
type groupTracker struct {
	podsC *informerCache
	rsC   *informerCache // deployments only
	owner types.UID      // "" = members are chosen by name (one pod)
	name  string         // the one pod (owner == "")
	uid   types.UID      // …and its UID: a same-named replacement is not it
	viaRS bool
	regs  []cache.ResourceEventHandlerRegistration

	kick    chan struct{}
	done    chan struct{}
	release func()

	mu      sync.Mutex
	members map[string]*memberPod // by pod UID
	synced  bool
	blind   string
	changed chan struct{} // closed and replaced on every change of the set
}

// trackWorkload / trackPod start a tracker; stop() releases everything.
func (s *session) trackWorkload(def *kindDef, ns string, owner types.UID) (*groupTracker, error) {
	t := &groupTracker{owner: owner, viaRS: def == deploymentsKind}
	return t, s.startTracker(t, cacheKey{gvr: podsKind.gvr, namespace: ns}, ns)
}

func (s *session) trackPod(ns, name string, uid types.UID) (*groupTracker, error) {
	t := &groupTracker{name: name, uid: uid}
	return t, s.startTracker(t, cacheKey{gvr: podsKind.gvr, namespace: ns, selector: "metadata.name=" + name}, ns)
}

func (s *session) startTracker(t *groupTracker, podsKey cacheKey, ns string) error {
	t.kick, t.done = make(chan struct{}, 1), make(chan struct{})
	t.members, t.changed = map[string]*memberPod{}, make(chan struct{})
	var leased []*informerCache
	t.release = func() {
		for i, c := range leased {
			if i < len(t.regs) {
				_ = c.inf.RemoveEventHandler(t.regs[i])
			}
			s.caches.release(c)
		}
	}
	h := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { t.poke() },
		UpdateFunc: func(any, any) { t.poke() },
		DeleteFunc: func(any) { t.poke() },
	}
	lease := func(key cacheKey, def *kindDef) (*informerCache, error) {
		c, ok := s.caches.acquire(key, def)
		if !ok {
			return nil, errSessionClosed
		}
		leased = append(leased, c)
		reg, err := c.inf.AddEventHandler(h)
		if err != nil {
			return nil, err
		}
		t.regs = append(t.regs, reg)
		return c, nil
	}
	var err error
	if t.podsC, err = lease(podsKey, podsKind); err != nil {
		t.release()
		return err
	}
	if t.viaRS {
		if t.rsC, err = lease(cacheKey{gvr: replicaSetsKind.gvr, namespace: ns}, replicaSetsKind); err != nil {
			t.release()
			return err
		}
	}
	go t.loop()
	return nil
}

var errSessionClosed = fmt.Errorf("session closed")

func (t *groupTracker) poke() {
	select {
	case t.kick <- struct{}{}:
	default:
	}
}

func (t *groupTracker) stop() {
	close(t.done)
	t.release()
}

// loop recomputes the member set on every kick, and checks the caches'
// sync/transport state while they are not synced (local state only).
func (t *groupTracker) loop() {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var failingSince time.Time
	for {
		select {
		case <-t.done:
			return
		case <-t.kick:
		case now := <-tick.C:
			t.setBlind(cacheProblem(now, &failingSince, t.podsC, t.rsC))
			if t.isSynced() {
				continue // changes come as kicks
			}
		}
		t.recompute()
	}
}

func (t *groupTracker) isSynced() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.synced
}

func (t *groupTracker) setBlind(reason string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.blind == reason {
		return
	}
	t.blind = reason
	for _, m := range t.members {
		m.box.setBlind(reason)
	}
	t.bumpLocked()
}

func (t *groupTracker) bumpLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

func controllerUID(o metav1.Object) types.UID {
	for _, r := range o.GetOwnerReferences() {
		if r.Controller != nil && *r.Controller {
			return r.UID
		}
	}
	return ""
}

// recompute rebuilds the member set from the caches. Only members whose
// resourceVersion changed are expanded (the rest of the namespace is read
// from typed metadata only).
func (t *groupTracker) recompute() {
	synced := true
	for _, r := range t.regs {
		synced = synced && r.HasSynced()
	}
	owners := map[types.UID]bool{}
	if t.owner != "" && !t.viaRS {
		owners[t.owner] = true
	}
	if t.viaRS {
		for _, obj := range t.rsC.inf.GetStore().List() {
			if o, ok := obj.(metav1.Object); ok && controllerUID(o) == t.owner {
				owners[o.GetUID()] = true
			}
		}
	}
	seen := map[string]bool{}
	t.mu.Lock()
	defer t.mu.Unlock()
	changed := false
	for _, obj := range t.podsC.inf.GetStore().List() {
		o, ok := obj.(metav1.Object)
		if !ok {
			continue
		}
		if t.owner != "" && !owners[controllerUID(o)] ||
			t.owner == "" && (o.GetName() != t.name || t.uid != "" && o.GetUID() != t.uid) {
			continue
		}
		uid := string(o.GetUID())
		seen[uid] = true
		m := t.members[uid]
		if m != nil && m.rv == o.GetResourceVersion() {
			continue
		}
		u, ok := asUnstructured(obj)
		if !ok {
			continue
		}
		if m == nil {
			m = &memberPod{uid: uid, name: o.GetName(), created: o.GetCreationTimestamp().Time, box: newObsBox()}
			if t.blind != "" {
				m.box.setBlind(t.blind)
			}
			t.members[uid] = m
			changed = true
		}
		m.rv = o.GetResourceVersion()
		obs := observePod(u)
		if ctrs := ctrOrder(u.Object); !slices.Equal(ctrs, m.ctrs) {
			m.ctrs = ctrs
			changed = true // a new channel (an ephemeral container) is a membership change
		}
		m.box.set(obs)
		if synced {
			m.box.setSynced()
		}
	}
	for uid, m := range t.members {
		if !seen[uid] {
			m.box.set(podObs{}) // deleted (its source ends)
			delete(t.members, uid)
			changed = true
		}
	}
	if synced && !t.synced {
		t.synced = true
		for _, m := range t.members {
			m.box.setSynced()
		}
		changed = true
	}
	if changed {
		t.bumpLocked()
	}
}

func ctrOrder(o map[string]any) []string {
	var out []string
	for _, ch := range podChannels(o, "spec") {
		out = append(out, ch.ID)
	}
	return out
}

// snapshot: copies of the members, newest first (then by name), and a
// channel closed on the next change of the set.
func (t *groupTracker) snapshot() (members []memberPod, synced bool, blind string, changed <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, m := range t.members {
		members = append(members, *m)
	}
	sort.Slice(members, func(i, j int) bool {
		if !members[i].created.Equal(members[j].created) {
			return members[i].created.After(members[j].created)
		}
		return members[i].name < members[j].name
	})
	return members, t.synced, t.blind, t.changed
}
