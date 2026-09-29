package kubernetes

import (
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Container kinds for log sources.
const (
	ctrRegular   = ""
	ctrInit      = "init"
	ctrSidecar   = "sidecar" // an init container with restartPolicy Always
	ctrEphemeral = "ephemeral"
)

// ctrObs is what a log source needs to know about its container.
type ctrObs struct {
	kind      string
	id        string // current runtime container ID; "" = never started
	restarts  int64
	running   bool
	startedAt time.Time // of the running instance
	waiting   string    // reason
	exited    bool      // current state terminated
	exitCode  int64
}

// podObs is one observation of a pod from the informer cache.
type podObs struct {
	exists        bool // false: deleted (or never seen)
	uid           string
	phase         string
	restartPolicy string
	deleting      bool
	ctrs          map[string]ctrObs
}

func observePod(u *unstructured.Unstructured) podObs {
	o := podObs{
		exists: true, uid: string(u.GetUID()), deleting: u.GetDeletionTimestamp() != nil,
		phase: str(u.Object, "status", "phase"), restartPolicy: str(u.Object, "spec", "restartPolicy"),
		ctrs: map[string]ctrObs{},
	}
	kinds := map[string]string{}
	for _, c := range slice(u.Object, "spec", "containers") {
		kinds[strOf(c, "name")] = ctrRegular
	}
	for _, c := range slice(u.Object, "spec", "initContainers") {
		k := ctrInit
		if strOf(c, "restartPolicy") == "Always" {
			k = ctrSidecar
		}
		kinds[strOf(c, "name")] = k
	}
	for _, c := range slice(u.Object, "spec", "ephemeralContainers") {
		kinds[strOf(c, "name")] = ctrEphemeral
	}
	for name, k := range kinds {
		o.ctrs[name] = ctrObs{kind: k}
	}
	for _, field := range []string{"containerStatuses", "initContainerStatuses", "ephemeralContainerStatuses"} {
		for _, cs := range slice(u.Object, "status", field) {
			name := strOf(cs, "name")
			c := o.ctrs[name]
			c.id = strOf(cs, "containerID")
			c.restarts = i64(cs, "restartCount")
			if st, ok := cs["state"].(map[string]any); ok {
				if r, ok := st["running"].(map[string]any); ok {
					c.running = true
					c.startedAt, _ = time.Parse(time.RFC3339, strOf(r, "startedAt"))
				}
				if w, ok := st["waiting"].(map[string]any); ok {
					c.waiting = strOf(w, "reason")
				}
				if t, ok := st["terminated"].(map[string]any); ok {
					c.exited = true
					c.exitCode = i64(t, "exitCode")
					if id := strOf(t, "containerID"); id != "" {
						c.id = id
					}
				}
			}
			if lt, ok := cs["lastState"].(map[string]any); ok && !c.running && !c.exited {
				// waiting after an exit (CrashLoopBackOff): the last instance
				if t, ok := lt["terminated"].(map[string]any); ok {
					if c.id == "" {
						c.id = strOf(t, "containerID")
					}
					c.exitCode = i64(t, "exitCode")
				}
			}
			o.ctrs[name] = c
		}
	}
	return o
}

func strOf(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// restartExpected: will the kubelet start this (exited) container again?
func (o podObs) restartExpected(c ctrObs) bool {
	if !o.exists || o.deleting || o.phase == "Succeeded" || o.phase == "Failed" {
		return false
	}
	switch c.kind {
	case ctrEphemeral:
		return false
	case ctrInit:
		return c.exitCode != 0 && o.restartPolicy != "Never"
	}
	switch o.restartPolicy {
	case "Never":
		return false
	case "OnFailure":
		return c.exitCode != 0
	}
	return true // Always (the default); sidecars always restart
}

// obsBox holds the latest observation of one pod: the informer handler
// sets it without blocking, a source reads it when it has to decide and
// waits on changed() (never on "the next event": the event it needs may
// already have happened).
type obsBox struct {
	mu      sync.Mutex
	v       podObs
	synced  bool   // the cache delivered its initial state
	blind   string // why observations are unavailable (e.g. no watch permission)
	changed chan struct{}
}

func newObsBox() *obsBox { return &obsBox{changed: make(chan struct{})} }

func (b *obsBox) set(v podObs) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.v = v
	b.bumpLocked()
}

func (b *obsBox) setSynced() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.synced {
		b.synced = true
		b.bumpLocked()
	}
}

// setBlind: tracking is not possible (reason) or possible again ("").
func (b *obsBox) setBlind(reason string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.blind != reason {
		b.blind = reason
		b.bumpLocked()
	}
}

func (b *obsBox) bumpLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// get returns the latest observation and a channel closed on the next
// change after it.
func (b *obsBox) get() (v podObs, synced bool, blind string, changed <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.v, b.synced, b.blind, b.changed
}
