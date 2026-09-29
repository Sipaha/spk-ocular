package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var _ provider.LogSource = (*session)(nil)

// defaultContainerAnnotation is kubectl's choice of a pod's default container.
const defaultContainerAnnotation = "kubectl.kubernetes.io/default-container"

// LogInfo reads the object once (UID-checked) for its containers.
func (s *session) LogInfo(ctx context.Context, ref core.Ref) (core.LogInfo, error) {
	u, err := s.getObject(ctx, ref)
	if err != nil {
		return core.LogInfo{}, err
	}
	switch ref.Kind {
	case podsKind.desc.ID:
		info := core.LogInfo{Channels: podChannels(u.Object, "spec"), Previous: true}
		info.DefaultChannel = defaultChannel(u, info.Channels)
		return info, nil
	}
	return core.LogInfo{}, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", ref.Kind)}
}

// getObject GETs ref's object and checks its UID.
func (s *session) getObject(ctx context.Context, ref core.Ref) (*unstructured.Unstructured, error) {
	def := s.kinds.byID[ref.Kind]
	if def == nil {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", ref.Kind)}
	}
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	u, err := s.dyn.Resource(def.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		class, msg := classify(err)
		return nil, &provider.Error{Class: class, Message: msg}
	}
	if ref.UID != "" && string(u.GetUID()) != ref.UID {
		return nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s was deleted and a new object took its name", ref)}
	}
	return u, nil
}

// podChannels lists the containers of a pod spec (at path) in kubectl's
// order: regular, init (sidecars marked), ephemeral.
func podChannels(o map[string]any, path ...string) []core.LogChannel {
	var out []core.LogChannel
	for _, c := range slice(o, append(path, "containers")...) {
		out = append(out, core.LogChannel{ID: strOf(c, "name"), Title: strOf(c, "name")})
	}
	for _, c := range slice(o, append(path, "initContainers")...) {
		note := ctrInit
		if strOf(c, "restartPolicy") == "Always" {
			note = ctrSidecar
		}
		out = append(out, core.LogChannel{ID: strOf(c, "name"), Title: strOf(c, "name"), Note: note})
	}
	for _, c := range slice(o, append(path, "ephemeralContainers")...) {
		out = append(out, core.LogChannel{ID: strOf(c, "name"), Title: strOf(c, "name"), Note: ctrEphemeral})
	}
	return out
}

func defaultChannel(u *unstructured.Unstructured, chs []core.LogChannel) string {
	if want := u.GetAnnotations()[defaultContainerAnnotation]; want != "" {
		for _, c := range chs {
			if c.ID == want {
				return want
			}
		}
	}
	for _, c := range chs {
		if c.Note == "" {
			return c.ID
		}
	}
	return ""
}

// StreamLogs streams ref's logs into sink.
func (s *session) StreamLogs(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	if s.logs == nil {
		return &provider.Error{Class: provider.ClassUnsupported, Message: "logs are not available for this context"}
	}
	switch ref.Kind {
	case podsKind.desc.ID:
		return s.streamPod(ctx, ref, q, sink)
	}
	return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", ref.Kind)}
}

func (s *session) streamPod(ctx context.Context, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	ctr := q.Channel
	if ctr == "" {
		info, err := s.LogInfo(ctx, ref)
		if err != nil {
			return err
		}
		ctr = info.DefaultChannel
	}
	if ctr == provider.ChannelAll {
		return &provider.Error{Class: provider.ClassUnsupported, Message: "all containers of a pod: not yet"}
	}
	if err := sink.Source(1, ref.UID+"/"+ctr, ref.Name+"/"+ctr, ctr); err != nil {
		return err
	}
	var box *obsBox
	if q.Follow {
		box = newObsBox()
		stop, err := s.observePod(ref.Scope, ref.Name, box)
		if err != nil {
			return err
		}
		defer stop()
	}
	src := newPodSource(1, ref.Scope, ref.Name, ref.UID, ctr, s.logs, box, sink, q)
	// One source: its backlog is simply the start of its stream.
	if err := sink.Ready(); err != nil {
		return err
	}
	return src.run(ctx)
}

// observeBlindAfter: how long a pod cache may fail before sources stop
// counting on it (a denied watch, an unreachable cluster).
const observeBlindAfter = 3 * time.Second

// observePod feeds box with the pod's observations from a cache narrowed to
// its name (the one an open details panel uses too) until stop.
func (s *session) observePod(ns, name string, box *obsBox) (stop func(), err error) {
	c, ok := s.caches.acquire(cacheKey{gvr: podsKind.gvr, namespace: ns, selector: "metadata.name=" + name}, podsKind)
	if !ok {
		return nil, &provider.Error{Class: provider.ClassGone, Message: "session closed"}
	}
	// The selector already narrows to the name; checked again anyway (a
	// shared or fake cache may hold more).
	mine := func(obj any) bool {
		if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			obj = d.Obj
		}
		o, ok := obj.(metav1.Object)
		return ok && o.GetName() == name && o.GetNamespace() == ns
	}
	set := func(obj any) {
		if u, ok := asUnstructured(obj); ok && mine(obj) {
			box.set(observePod(u))
		}
	}
	reg, err := c.inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    set,
		UpdateFunc: func(_, obj any) { set(obj) },
		DeleteFunc: func(obj any) {
			if mine(obj) {
				box.set(podObs{})
			}
		},
	})
	if err != nil {
		s.caches.release(c)
		return nil, &provider.Error{Class: provider.ClassGone, Message: err.Error()}
	}
	done := make(chan struct{})
	go watchObservation(c, reg, box, done)
	return func() {
		close(done)
		_ = c.inf.RemoveEventHandler(reg)
		s.caches.release(c)
	}, nil
}

// watchObservation marks box synced once the handler got the initial state,
// and blind while the cache cannot load (e.g. no permission to watch pods:
// pods/log may still be allowed). Local state only — no cluster calls.
func watchObservation(c *informerCache, reg cache.ResourceEventHandlerRegistration, box *obsBox, done <-chan struct{}) {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	var failingSince time.Time
	for {
		select {
		case <-done:
			return
		case <-c.stop:
			box.setBlind("the pod list was closed")
			return
		case now := <-t.C:
			if reg.HasSynced() {
				box.setBlind("")
				box.setSynced()
				return
			}
			tr := c.transport()
			switch {
			case !tr.failing:
				failingSince = time.Time{}
			case failingSince.IsZero():
				failingSince = now
			case now.Sub(failingSince) >= observeBlindAfter:
				box.setBlind(strings.TrimSpace(fmt.Sprintf("%s: %s", tr.class, tr.message)))
			}
		}
	}
}
