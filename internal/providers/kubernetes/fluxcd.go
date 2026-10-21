package kubernetes

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// FluxCD reads only discovered controller resources and related Events. It never
// follows kubeConfig, valuesFrom, decryption, webhook or Secret references.
func (s *session) FluxCD(parent context.Context, scope core.ScopeSel) (core.FluxSnapshot, error) {
	ctx, release, err := s.inventoryContext(parent)
	if err != nil {
		return core.FluxSnapshot{}, err
	}
	defer release()
	out := core.FluxSnapshot{Objects: []core.FluxObject{}, Events: []core.ArgoEvent{}, Coverage: []core.ArgoCoverage{}, Discovery: s.cat.snap().state}
	spaces := []string{""}
	if scope.Mode != core.ScopeAll {
		spaces = scope.SelectedNames()
	}
	if len(spaces) > 128 {
		spaces = spaces[:128]
		out.Truncated = true
	}
	type source struct {
		gvr           schema.GroupVersionResource
		kind, section string
	}
	sources := []source{}
	for _, k := range core.FluxResourceKinds {
		if d := s.kind(k.Group + "/" + k.Resource); d != nil && d.discovered && d.namespaced && d.kind == k.Kind {
			sources = append(sources, source{d.gvr, k.Kind, k.Section})
		}
	}
	if len(sources) > 0 {
		sources = append(sources, source{schema.GroupVersionResource{Version: "v1", Resource: "events"}, "Event", ""})
	}
	count, bytes, pages := 0, 0, 0
	seen := map[string]bool{}
	for _, src := range sources {
		for _, ns := range spaces {
			state := "ready"
			cursor := inventoryCursor{}
			job, done := context.WithTimeout(ctx, 15*time.Second)
			for page := 0; ; page++ {
				if err := ctx.Err(); err != nil {
					done()
					return out, err
				}
				if count >= 2000 || bytes >= 8<<20 || pages >= 2048 || page >= 256 {
					out.Truncated = true
					state = "partial"
					break
				}
				pages++
				list, e := s.dyn.Resource(src.gvr).Namespace(ns).List(job, metav1.ListOptions{Limit: 50, Continue: cursor.token})
				if e != nil {
					class, _ := classify(e)
					state = string(class)
					break
				}
				for i := range list.Items {
					u := &list.Items[i]
					if !scope.Contains(u.GetNamespace()) || u.GetUID() == "" || u.GetAPIVersion() != src.gvr.GroupVersion().String() || u.GetKind() != src.kind {
						continue
					}
					raw, e := json.Marshal(u.Object)
					if e != nil {
						out.Truncated = true
						state = "partial"
						continue
					}
					bytes += len(raw)
					if len(raw) > 1<<20 || bytes > 8<<20 || count >= 2000 {
						out.Truncated = true
						state = "partial"
						break
					}
					key := src.kind + "/" + u.GetNamespace() + "/" + u.GetName() + "/" + string(u.GetUID())
					if seen[key] {
						out.Truncated = true
						state = "partial"
						continue
					}
					seen[key] = true
					count++
					if src.kind == "Event" {
						if event, ok := s.fluxEvent(u); ok {
							out.Events = append(out.Events, event)
						}
					} else {
						o := s.fluxObject(u, src.section)
						out.Objects = append(out.Objects, o)
						out.Truncated = out.Truncated || o.Truncated
					}
				}
				more, repeated := cursor.advance(list.GetContinue())
				if repeated {
					out.Truncated = true
					state = "partial"
				}
				if !more || state == "partial" {
					break
				}
			}
			done()
			out.Coverage = append(out.Coverage, core.ArgoCoverage{Kind: src.kind, Scope: ns, State: state})
		}
	}
	current := map[string]bool{}
	for _, o := range out.Objects {
		current[argoRefKey(o.Ref)] = true
	}
	for i := range out.Events {
		out.Events[i].Openable = current[argoRefKey(out.Events[i].Ref)]
	}
	sort.Slice(out.Objects, func(i, j int) bool { return argoRefKey(out.Objects[i].Ref) < argoRefKey(out.Objects[j].Ref) })
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].At > out.Events[j].At })
	if len(out.Events) > 500 {
		out.Events = out.Events[:500]
		out.Truncated = true
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func fluxKindID(group, kind string) string {
	for _, k := range core.FluxResourceKinds {
		if k.Kind == kind && (group == "" || group == k.Group) {
			return k.Group + "/" + k.Resource
		}
	}
	return ""
}
func fluxInt(o map[string]any, path ...string) int64 {
	n, _, _ := unstructured.NestedInt64(o, path...)
	return n
}
func (s *session) fluxObject(u *unstructured.Unstructured, section string) core.FluxObject {
	o := u.Object
	kind := u.GetKind()
	id := fluxKindID(u.GroupVersionKind().Group, kind)
	observed := fluxInt(o, "status", "observedGeneration")
	out := core.FluxObject{Ref: core.Ref{Provider: ProviderID, Target: s.target, Kind: id, Scope: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID())}, Kind: kind, Section: section, State: "Unknown", Generation: u.GetGeneration(), ObservedGeneration: observed, Conditions: []core.FluxCondition{}, Links: []core.FluxLink{}, Inventory: []core.FluxResource{}, LocalDestination: fieldAt(o, "spec", "kubeConfig") == nil, CreatedAt: u.GetCreationTimestamp().UnixMilli()}
	if u.GetCreationTimestamp().Time.IsZero() {
		out.CreatedAt = 0
	}
	conditions := slice(o, "status", "conditions")
	conditionsTruncated := len(conditions) > 32
	if len(conditions) > 32 {
		conditions = conditions[:32]
		out.Truncated = true
	}
	ready, reconciling, failed, stalled := false, false, false, false
	messages := map[string]string{}
	for _, c := range conditions {
		generation := fluxInt(c, "observedGeneration")
		if generation == 0 {
			generation = observed
		}
		condition := core.FluxCondition{Type: argoString(c, "type"), Status: argoString(c, "status"), Reason: argoString(c, "reason"), Message: argoString(c, "message"), ObservedGeneration: generation}
		out.Conditions = append(out.Conditions, condition)
		if generation != u.GetGeneration() || generation <= 0 {
			continue
		}
		messages[condition.Type] = condition.Message
		switch {
		case condition.Type == "Stalled" && condition.Status == "True":
			stalled = true
			out.Message = condition.Message
		case condition.Type == "Reconciling" && condition.Status == "True":
			reconciling = true
			out.Message = condition.Message
		case condition.Type == "Ready" && condition.Status == "True":
			ready = true
		case condition.Type == "Ready" && condition.Status == "False":
			failed = true
			out.Message = condition.Message
		}
	}
	suspended, _, _ := unstructured.NestedBool(o, "spec", "suspend")
	switch {
	case suspended:
		out.State = "Suspended"
		out.Message = ""
	case conditionsTruncated:
		out.State = "Unknown"
		out.Message = ""
	case stalled:
		out.State = "Failed"
		out.Message = messages["Stalled"]
	case reconciling:
		out.State = "Reconciling"
		out.Message = messages["Reconciling"]
	case failed:
		out.State = "Failed"
		out.Message = messages["Ready"]
	case ready:
		out.State = "Ready"
		out.Message = messages["Ready"]
	}
	if kind == "GitRepository" || kind == "OCIRepository" || kind == "HelmRepository" {
		out.URL = argoURL(str(o, "spec", "url"))
	}
	out.Path = argoString(o, "spec", "path")
	out.Revision = argoString(o, "status", "artifact", "revision")
	if out.Revision == "" {
		out.Revision = argoString(o, "status", "lastAppliedRevision")
	}
	if out.Revision == "" {
		out.Revision = argoString(o, "status", "lastAttemptedRevision")
	}
	// Only typed controller references are exposed; no Secret/config references.
	add := func(rel string, m map[string]any, defaultKind string) {
		name := str(m, "name")
		k := str(m, "kind")
		if k == "" {
			k = defaultKind
		}
		ns := str(m, "namespace")
		if ns == "" {
			ns = u.GetNamespace()
		}
		group := strings.Split(str(m, "apiVersion"), "/")[0]
		if str(m, "apiVersion") == "" {
			group = ""
		}
		id := fluxKindID(group, k)
		if name == "" || len(name) > 253 || len(ns) > 253 || id == "" {
			return
		}
		group = strings.Split(id, "/")[0]
		out.Links = append(out.Links, core.FluxLink{Relation: rel, Kind: k, Group: group, Namespace: ns, Name: name})
	}
	mapping := func(path ...string) map[string]any { m, _ := fieldAt(o, path...).(map[string]any); return m }
	if kind == "Kustomization" || kind == "HelmChart" || kind == "ImageUpdateAutomation" {
		add("source", mapping("spec", "sourceRef"), "GitRepository")
	}
	if kind == "HelmRelease" {
		if fieldAt(o, "spec", "chartRef") != nil {
			add("source", mapping("spec", "chartRef"), "HelmChart")
		} else {
			add("source", mapping("spec", "chart", "spec", "sourceRef"), "HelmRepository")
		}
	}
	if kind == "ImagePolicy" {
		add("source", mapping("spec", "imageRepositoryRef"), "ImageRepository")
	}
	if kind == "Alert" {
		add("provider", mapping("spec", "providerRef"), "Provider")
	}
	dependencies := slice(o, "spec", "dependsOn")
	if len(dependencies) > 64 {
		dependencies = dependencies[:64]
		out.Truncated = true
	}
	for _, d := range dependencies {
		add("dependency", d, kind)
	}
	if kind == "Receiver" {
		refs := slice(o, "spec", "resources")
		if len(refs) > 64 {
			refs = refs[:64]
			out.Truncated = true
		}
		for _, r := range refs {
			add("resource", r, "")
		}
	}
	if kind == "Kustomization" {
		entries := slice(o, "status", "inventory", "entries")
		if len(entries) > 500 {
			entries = entries[:500]
			out.Truncated = true
		}
		for _, entry := range entries {
			parts := strings.Split(str(entry, "id"), "_")
			if len(parts) != 4 || parts[1] == "" || parts[3] == "" || len(parts[0]) > 63 || len(parts[1]) > 253 || len(parts[2]) > 253 || len(parts[3]) > 63 || len(str(entry, "v")) > 63 {
				out.Truncated = true
				continue
			}
			out.Inventory = append(out.Inventory, core.FluxResource{Namespace: parts[0], Name: parts[1], Group: parts[2], Kind: parts[3], Version: argoString(entry, "v")})
		}
	}
	// Detect bounded text projection losses so a shortened identifier is never a link.
	for _, c := range out.Conditions {
		out.Truncated = out.Truncated || strings.Contains(c.Message, "…") || strings.Contains(c.Reason, "…")
	}
	out.Truncated = out.Truncated || strings.Contains(out.URL, "…") || strings.Contains(out.Revision, "…")
	return out
}
func (s *session) fluxEvent(u *unstructured.Unstructured) (core.ArgoEvent, bool) {
	o := u.Object
	group := strings.Split(str(o, "involvedObject", "apiVersion"), "/")[0]
	kind := str(o, "involvedObject", "kind")
	id := fluxKindID(group, kind)
	if id == "" {
		return core.ArgoEvent{}, false
	}
	ref := core.Ref{Provider: ProviderID, Target: s.target, Kind: id, Scope: str(o, "involvedObject", "namespace"), Name: str(o, "involvedObject", "name"), UID: str(o, "involvedObject", "uid")}
	if ref.Scope != u.GetNamespace() || ref.Name == "" || ref.UID == "" {
		return core.ArgoEvent{}, false
	}
	at := str(o, "lastTimestamp")
	if at == "" {
		at = str(o, "eventTime")
	}
	if at == "" {
		at = str(o, "metadata", "creationTimestamp")
	}
	parsed, _ := time.Parse(time.RFC3339Nano, at)
	ms := int64(0)
	if !parsed.IsZero() {
		ms = parsed.UnixMilli()
	}
	return core.ArgoEvent{ID: string(u.GetUID()), Ref: ref, Type: argoString(o, "type"), Reason: argoString(o, "reason"), Message: argoString(o, "message"), At: ms, Count: fluxInt(o, "count")}, true
}
