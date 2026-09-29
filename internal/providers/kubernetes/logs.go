package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
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
	return logInfoOf(s.kinds.byID[ref.Kind], u)
}

func logInfoOf(def *kindDef, u *unstructured.Unstructured) (core.LogInfo, error) {
	switch {
	case def == podsKind:
		info := core.LogInfo{Channels: podChannels(u.Object, "spec"), Previous: true}
		info.DefaultChannel = defaultChannel(u.GetAnnotations(), info.Channels)
		return info, nil
	case logWorkloads[def]:
		info := core.LogInfo{Channels: podChannels(u.Object, "spec", "template", "spec"), Aggregate: true}
		ann, _, _ := unstructured.NestedStringMap(u.Object, "spec", "template", "metadata", "annotations")
		info.DefaultChannel = defaultChannel(ann, info.Channels)
		return info, nil
	}
	return core.LogInfo{}, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", def.desc.ID)}
}

// logWorkloads: kinds whose logs are their pods' (by controller UID).
var logWorkloads = map[*kindDef]bool{deploymentsKind: true, statefulSetsKind: true, daemonSetsKind: true, replicaSetsKind: true}

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

func defaultChannel(annotations map[string]string, chs []core.LogChannel) string {
	if want := annotations[defaultContainerAnnotation]; want != "" {
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
	switch def := s.kinds.byID[ref.Kind]; {
	case def == podsKind:
		return s.streamPod(ctx, ref, q, sink)
	case logWorkloads[def]:
		return s.streamWorkload(ctx, def, ref, q, sink)
	}
	return &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s have no logs", ref.Kind)}
}

func (s *session) streamWorkload(ctx context.Context, def *kindDef, ref core.Ref, q provider.LogQuery, sink provider.LogSink) error {
	if q.Previous {
		return &provider.Error{Class: provider.ClassUnsupported, Message: "previous logs are shown for one pod at a time"}
	}
	u, err := s.getObject(ctx, ref) // UID-checked
	if err != nil {
		return err
	}
	info, err := logInfoOf(def, u)
	if err != nil {
		return err
	}
	tr, err := s.trackWorkload(def, ref.Scope, u.GetUID())
	if err != nil {
		return &provider.Error{Class: provider.ClassGone, Message: err.Error()}
	}
	defer tr.stop()
	g := &logGroup{s: s, ns: ref.Scope, q: q, sink: &lockedSink{sink: sink}, tr: tr, channel: channelsFor(q.Channel, info.DefaultChannel)}
	return g.run(ctx)
}

// channelsFor picks a member's channels: all, the default, or one.
func channelsFor(want, def string) func(memberPod) []string {
	return func(m memberPod) []string {
		switch want {
		case provider.ChannelAll:
			return m.ctrs
		case "":
			return []string{def}
		}
		return []string{want}
	}
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
		tr, err := s.trackPod(ref.Scope, ref.Name, types.UID(ref.UID))
		if err != nil {
			return &provider.Error{Class: provider.ClassGone, Message: err.Error()}
		}
		defer tr.stop()
		g := &logGroup{s: s, ns: ref.Scope, q: q, sink: &lockedSink{sink: sink}, tr: tr, channel: channelsFor(ctr, "")}
		return g.run(ctx)
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
	src.slots = s.slots
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

// watchObservation marks box synced once the handler got the initial
// state, and blind while the cache's list/watch keeps failing (e.g. pods
// may be listed but not watched: the list "syncs", yet no change would ever
// arrive). Local state only — no cluster calls.
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
				box.setSynced()
			}
			box.setBlind(cacheProblem(now, &failingSince, c))
		}
	}
}

// cacheProblem describes caches that have failed for observeBlindAfter
// ("" while they work).
func cacheProblem(now time.Time, since *time.Time, cs ...*informerCache) string {
	var bad []string
	for _, c := range cs {
		if c == nil {
			continue
		}
		if tr := c.transport(); tr.failing {
			bad = append(bad, fmt.Sprintf("%s: %s", tr.class, tr.message))
		}
	}
	if len(bad) == 0 {
		*since = time.Time{}
		return ""
	}
	if since.IsZero() {
		*since = now
	}
	if now.Sub(*since) < observeBlindAfter {
		return ""
	}
	return strings.Join(bad, "; ")
}
