package kubernetes

import (
	"context"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/spk/spk-ocular/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const timelineEventLimit = 10000
const timelineResourceLimit = 30000
const timelineMessageLimit = 2048

// Timeline reads Events and only metadata for workload ownership. No manifests,
// logs, configuration values or Secret inventories are requested. Refreshes share
// the inventory gate, and disconnect cancels an in-flight snapshot.
func (s *session) Timeline(parent context.Context, scope core.ScopeSel) (core.Timeline, error) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stop := context.AfterFunc(s.ctx, cancel)
	defer stop()
	select {
	case s.graphGate <- struct{}{}:
		defer func() { <-s.graphGate }()
	case <-ctx.Done():
		return core.Timeline{}, ctx.Err()
	}
	snap := s.cat.snap()
	out := core.Timeline{Events: []core.TimelineEvent{}, Resources: []core.TimelineResource{}, Problems: []core.GraphProblem{}, Discovery: snap.state}
	namespaces := []string{""}
	if scope.Mode != core.ScopeAll {
		namespaces = scope.SelectedNames()
	}
	defs := []*kindDef{eventsKind}
	for _, d := range snap.reg.list {
		switch d.gvr.Group + "/" + d.gvr.Resource {
		case "/pods", "apps/deployments", "apps/replicasets", "apps/statefulsets", "apps/daemonsets", "batch/jobs", "batch/cronjobs":
			defs = append(defs, d)
		}
	}
	jobs := 0
	for _, def := range defs {
		for _, ns := range namespaces {
			if jobs >= 4096 {
				out.Truncated = true
				break
			}
			jobs++
			if err := ctx.Err(); err != nil {
				return out, err
			}
			jobCtx, done := context.WithTimeout(ctx, 20*time.Second)
			token := ""
			seen := map[string]bool{}
			for {
				if (def == eventsKind && len(out.Events) >= timelineEventLimit) || (def != eventsKind && len(out.Resources) >= timelineResourceLimit) {
					out.Truncated = true
					break
				}
				options := metav1.ListOptions{Limit: 500, Continue: token}
				var next string
				var err error
				if def == eventsKind {
					var list *unstructured.UnstructuredList
					list, err = s.dyn.Resource(def.gvr).Namespace(ns).List(jobCtx, options)
					if err == nil {
						next = list.GetContinue()
						for i := range list.Items {
							u := &list.Items[i]
							if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
								continue
							}
							if len(out.Events) >= timelineEventLimit {
								out.Truncated = true
								break
							}
							out.Events = append(out.Events, s.timelineEvent(u))
						}
					}
				} else if s.graphMetadata != nil {
					var list *metav1.PartialObjectMetadataList
					list, err = s.graphMetadata.Resource(def.gvr).Namespace(ns).List(jobCtx, options)
					if err == nil {
						next = list.GetContinue()
						for i := range list.Items {
							u := &list.Items[i]
							if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
								continue
							}
							if len(out.Resources) >= timelineResourceLimit {
								out.Truncated = true
								break
							}
							owner := ""
							if c := metav1.GetControllerOfNoCopy(u); c != nil {
								owner = string(c.UID)
							}
							out.Resources = append(out.Resources, core.TimelineResource{Ref: s.ref(def, u.GetNamespace(), u.GetName(), string(u.GetUID())), KindTitle: kindOf(def), OwnerUID: owner})
						}
					}
				} else {
					// Dynamic fake sessions have no HTTP metadata client. Production sessions
					// always initialize it; the fallback still exports metadata only.
					var list *unstructured.UnstructuredList
					list, err = s.dyn.Resource(def.gvr).Namespace(ns).List(jobCtx, options)
					if err == nil {
						next = list.GetContinue()
						for i := range list.Items {
							u := &list.Items[i]
							if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" {
								continue
							}
							if len(out.Resources) >= timelineResourceLimit {
								out.Truncated = true
								break
							}
							owner := ""
							if c := metav1.GetControllerOfNoCopy(u); c != nil {
								owner = string(c.UID)
							}
							out.Resources = append(out.Resources, core.TimelineResource{Ref: s.ref(def, u.GetNamespace(), u.GetName(), string(u.GetUID())), KindTitle: kindOf(def), OwnerUID: owner})
						}
					}
				}
				if err != nil {
					class, _ := classify(err)
					out.Problems = append(out.Problems, core.GraphProblem{Kind: def.desc.ID, Scope: ns, Class: string(class)})
					break
				}
				if next == "" {
					break
				}
				if seen[next] {
					out.Truncated = true
					break
				}
				seen[next] = true
				token = next
			}
			done()
		}
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	sort.Slice(out.Events, func(i, j int) bool {
		if out.Events[i].LastAt != out.Events[j].LastAt {
			return out.Events[i].LastAt < out.Events[j].LastAt
		}
		return out.Events[i].ID < out.Events[j].ID
	})
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}

func (s *session) timelineEvent(u *unstructured.Unstructured) core.TimelineEvent {
	last, fallback := eventLastSeen(u)
	first := timeAt(u.Object, "firstTimestamp")
	if first.IsZero() {
		first = timeAtMicro(u.Object, "eventTime")
	}
	if first.IsZero() {
		first = last
		fallback = true
	}
	if !last.IsZero() && first.After(last) {
		first = last
		fallback = true
	}
	subject := eventSubject(s, u)
	message := str(u.Object, "message")
	truncated := len(message) > timelineMessageLimit
	if truncated {
		message = message[:timelineMessageLimit]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
	}
	return core.TimelineEvent{ID: string(u.GetUID()), Ref: s.ref(eventsKind, u.GetNamespace(), u.GetName(), string(u.GetUID())), Subject: subject.Ref, SubjectKind: str(u.Object, "involvedObject", "kind"), Openable: !subject.Inert,
		Type: nonEmpty(str(u.Object, "type"), "Normal"), Reason: str(u.Object, "reason"), Message: message, Count: eventCount(u.Object), FirstAt: unixMs(first), LastAt: unixMs(last), TimeFallback: fallback, MessageTruncated: truncated}
}
