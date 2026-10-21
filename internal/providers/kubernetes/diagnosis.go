package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Diagnosis is deliberately separate from Get and table observations. No
// dependency read is admitted until the user explicitly asks for this snapshot.
func (s *session) DiagnoseResource(parent context.Context, ref core.Ref) (core.Diagnosis, error) {
	out := core.Diagnosis{Ref: ref, Evidence: []core.DiagnosisEvidence{}, Coverage: []core.DiagnosisCoverage{}}
	if ref.Provider != ProviderID || ref.Target != s.target || ref.UID == "" || ref.Scope == "" || ref.Name == "" {
		return out, &provider.Error{Class: provider.ClassInvalid, Message: "diagnosis requires a namespaced UID-pinned reference"}
	}
	def := s.kind(ref.Kind)
	if def == nil || !def.namespaced || !diagnosisSupported(def.gvr.GroupResource()) {
		return out, &provider.Error{Class: provider.ClassUnsupported, Message: "resource diagnosis does not cover this kind"}
	}
	bounded, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	ctx, release, err := s.inventoryContext(bounded)
	if err != nil {
		return out, err
	}
	defer release()
	// The session transport caps each finite response before JSON allocation.
	ctx = newScopeBudget().context(ctx)
	read := func() (*unstructured.Unstructured, error) {
		u, e := s.dyn.Resource(def.gvr).Namespace(ref.Scope).Get(ctx, ref.Name, metav1.GetOptions{})
		if e != nil {
			class, _ := classify(e)
			return nil, &provider.Error{Class: class, Message: "diagnosis subject could not be read"}
		}
		if string(u.GetUID()) != ref.UID || u.GetNamespace() != ref.Scope || u.GetName() != ref.Name || u.GetAPIVersion() != def.gvr.GroupVersion().String() || u.GetKind() != kindOf(def) {
			return nil, &provider.Error{Class: provider.ClassGone, Message: "diagnosis subject was replaced"}
		}
		return u, nil
	}
	u, err := read()
	if err != nil {
		return out, err
	}
	d := diagnosisReader{s: s, ctx: ctx, out: &out}
	if !d.admit(u) {
		return out, &provider.Error{Class: provider.ClassScopeLimit, Message: "diagnosis subject exceeds the snapshot budget"}
	}
	d.object(u, ref)
	if ref.Kind == "pods" {
		d.references(u.Object, ref, "spec")
	}
	switch def.gvr.GroupResource() {
	case schema.GroupResource{Group: "batch", Resource: "jobs"}:
		d.references(u.Object, ref, "spec", "template", "spec")
		d.jobPods(u, ref)
	case schema.GroupResource{Group: "apps", Resource: "deployments"}, schema.GroupResource{Group: "apps", Resource: "replicasets"}, schema.GroupResource{Group: "apps", Resource: "statefulsets"}, schema.GroupResource{Group: "apps", Resource: "daemonsets"}:
		d.references(u.Object, ref, "spec", "template", "spec")
	case schema.GroupResource{Group: "batch", Resource: "cronjobs"}:
		d.references(u.Object, ref, "spec", "jobTemplate", "spec", "template", "spec")
		d.cover("schedule", "not_covered")
	}
	d.events(ref)
	// A replacement racing with dependency reads invalidates the entire snapshot.
	if _, err = read(); err != nil {
		return core.Diagnosis{}, err
	}
	if err = ctx.Err(); err != nil {
		return core.Diagnosis{}, err
	}
	out.CapturedAt = s.now().UnixMilli()
	return out, nil
}
func diagnosisSupported(gr schema.GroupResource) bool {
	switch gr {
	case schema.GroupResource{Resource: "pods"}, schema.GroupResource{Resource: "persistentvolumeclaims"}, schema.GroupResource{Group: "batch", Resource: "jobs"}, schema.GroupResource{Group: "batch", Resource: "cronjobs"}, schema.GroupResource{Group: "autoscaling", Resource: "horizontalpodautoscalers"}, schema.GroupResource{Group: "apps", Resource: "deployments"}, schema.GroupResource{Group: "apps", Resource: "replicasets"}, schema.GroupResource{Group: "apps", Resource: "statefulsets"}, schema.GroupResource{Group: "apps", Resource: "daemonsets"}:
		return true
	}
	return false
}

type diagnosisReader struct {
	s                              *session
	ctx                            context.Context
	out                            *core.Diagnosis
	bytes, objects, referencesRead int
}

func (d *diagnosisReader) cover(source, state string) {
	d.out.Coverage = append(d.out.Coverage, core.DiagnosisCoverage{Source: source, State: state})
	if state == "partial" {
		d.out.Truncated = true
	}
}
func (d *diagnosisReader) admit(u *unstructured.Unstructured) bool {
	size, err := encodedJSONSize(u.Object)
	if err != nil || size > 1<<20 || d.bytes+size > 4<<20 || d.objects >= 256 {
		d.out.Truncated = true
		return false
	}
	d.bytes += size
	d.objects++
	return true
}
func (d *diagnosisReader) evidence(e core.DiagnosisEvidence) {
	if len(d.out.Evidence) >= 256 {
		d.out.Truncated = true
		return
	}
	for _, p := range []*string{&e.Reason, &e.Message} {
		if len(*p) > 2048 {
			*p = (*p)[:2048] + "…"
			d.out.Truncated = true
		}
	}
	d.out.Evidence = append(d.out.Evidence, e)
}
func (d *diagnosisReader) object(u *unstructured.Unstructured, ref core.Ref) {
	health := genericHealth(u)
	if ref.Kind == "pods" {
		_, health, _ = projectPod(u, d.s.now())
	}
	if def := d.s.kind(ref.Kind); def != nil && !def.discovered && def.project != nil && ref.Kind != "pods" {
		_, health, _ = def.project(u, d.s.now())
	}
	issues := health.Issues
	if len(issues) == 0 {
		issues = []core.Issue{{State: health.State, Reason: health.Reason, Message: health.Message}}
	}
	for _, i := range issues {
		d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "health", Field: "status", State: i.State, Reason: i.Reason, Message: i.Message, At: i.Since})
	}
	// Raw condition observations retain independent reasons and the exact field.
	conditions := slice(u.Object, "status", "conditions")
	if len(conditions) > 32 {
		conditions = conditions[:32]
		d.out.Truncated = true
	}
	for i, c := range conditions {
		message := str(c, "message")
		reason := nonEmpty(str(c, "reason"), str(c, "type"))
		d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "condition", Field: fmt.Sprintf("status.conditions[%d]", i), State: core.HealthUnknown, Reason: reason, Message: str(c, "type") + "=" + str(c, "status") + "; " + message, At: unixMs(timeAt(c, "lastTransitionTime"))})
	}
	if ref.Kind == "pods" {
		for _, field := range []string{"initContainerStatuses", "containerStatuses", "ephemeralContainerStatuses"} {
			statuses := slice(u.Object, "status", field)
			if len(statuses) > 32 {
				statuses = statuses[:32]
				d.out.Truncated = true
			}
			for i, c := range statuses {
				for _, state := range []string{"state", "lastState"} {
					for _, phase := range []string{"waiting", "terminated"} {
						v, ok := fieldAt(c, state, phase).(map[string]any)
						if !ok {
							continue
						}
						reason := str(v, "reason")
						message := str(v, "message")
						if phase == "terminated" {
							message = fmt.Sprintf("exitCode=%d; %s", i64(v, "exitCode"), message)
						}
						d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "container", Field: fmt.Sprintf("status.%s[%d].%s.%s", field, i, state, phase), State: core.HealthUnknown, Reason: nonEmpty(reason, phase), Message: message, At: unixMs(timeAt(v, "finishedAt")), Container: str(c, "name"), Previous: state == "lastState"})
					}
				}
			}
		}
	}
	d.cover(ref.String()+"/status", "ready")
}

// list is finite even if a server ignores selectors, limits or continuation
// progress. Every consumer must independently check identity and ownership.
func (d *diagnosisReader) list(gvr schema.GroupVersionResource, ns, selector, labels string, accept func(*unstructured.Unstructured)) string {
	cursor := inventoryCursor{}
	for page := 0; page < 4; page++ {
		if d.ctx.Err() != nil {
			return "unavailable"
		}
		items, err := d.s.dyn.Resource(gvr).Namespace(ns).List(d.ctx, metav1.ListOptions{Limit: 32, Continue: cursor.token, FieldSelector: selector, LabelSelector: labels})
		if err != nil {
			class, _ := classify(err)
			return string(class)
		}
		for i := range items.Items {
			if !d.admit(&items.Items[i]) {
				return "partial"
			}
			accept(&items.Items[i])
		}
		more, repeated := cursor.advance(items.GetContinue())
		if repeated {
			return "partial"
		}
		if !more {
			return "ready"
		}
	}
	return "partial"
}
func (d *diagnosisReader) events(ref core.Ref) {
	state := d.list(schema.GroupVersionResource{Version: "v1", Resource: "events"}, ref.Scope, "involvedObject.uid="+ref.UID, "", func(u *unstructured.Unstructured) {
		o := u.Object
		if u.GetKind() != "Event" || u.GetAPIVersion() != "v1" || u.GetNamespace() != ref.Scope || u.GetUID() == "" || str(o, "involvedObject", "uid") != ref.UID || str(o, "involvedObject", "name") != ref.Name || str(o, "involvedObject", "namespace") != ref.Scope {
			return
		}
		source := core.Ref{Provider: ProviderID, Target: d.s.target, Scope: ref.Scope, Kind: "events", Name: u.GetName(), UID: string(u.GetUID())}
		at := timeAt(o, "series", "lastObservedTime")
		if at.IsZero() {
			at = timeAt(o, "lastTimestamp")
		}
		if at.IsZero() {
			at = timeAt(o, "eventTime")
		}
		d.evidence(core.DiagnosisEvidence{Ref: ref, Source: source, Rule: "event", Field: "message", State: core.HealthUnknown, Reason: str(o, "reason"), Message: str(o, "type") + "; " + str(o, "message"), At: unixMs(at)})
	})
	d.cover(ref.String()+"/events", state)
}
func (d *diagnosisReader) jobPods(_ *unstructured.Unstructured, ref core.Ref) {
	count := 0
	// Manual selectors can omit the standard Job labels. A bounded namespace
	// read keeps owner UID as the authority without assuming those labels.
	state := d.list(podsKind.gvr, ref.Scope, "", "", func(u *unstructured.Unstructured) {
		if u.GetKind() != "Pod" || u.GetAPIVersion() != "v1" || u.GetNamespace() != ref.Scope || u.GetUID() == "" {
			return
		}
		owned := false
		for _, o := range u.GetOwnerReferences() {
			if string(o.UID) == ref.UID && o.Kind == "Job" && o.APIVersion == "batch/v1" && o.Controller != nil && *o.Controller {
				owned = true
			}
		}
		if !owned {
			return
		}
		if count >= 16 {
			d.out.Truncated = true
			return
		}
		count++
		podRef := core.Ref{Provider: ProviderID, Target: d.s.target, Scope: ref.Scope, Kind: "pods", Name: u.GetName(), UID: string(u.GetUID())}
		d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "owned_pod", Field: "metadata.ownerReferences", State: core.HealthUnknown, Reason: "OwnedPod", Related: &podRef})
		d.object(u, podRef)
		d.references(u.Object, podRef, "spec")
		d.events(podRef)
	})
	if count >= 16 && d.out.Truncated {
		state = "partial"
	}
	d.cover(ref.String()+"/pods", state)
}

type diagnosisReference struct {
	kind, name, field string
	optional, key     bool
}

func (d *diagnosisReader) references(o map[string]any, ref core.Ref, path ...string) {
	spec, ok := fieldAt(o, path...).(map[string]any)
	if !ok {
		d.cover(ref.String()+"/references", "not_covered")
		return
	}
	refs := []diagnosisReference{}
	add := func(kind string, m map[string]any, nameField, field string, key bool) {
		if name := str(m, nameField); name != "" {
			optional, _, _ := unstructured.NestedBool(m, "optional")
			refs = append(refs, diagnosisReference{kind, name, strings.Join(path, ".") + "." + field, optional, key})
		}
	}
	for _, group := range []string{"containers", "initContainers", "ephemeralContainers"} {
		for i, c := range slice(spec, group) {
			base := fmt.Sprintf("%s[%d]", group, i)
			for j, e := range slice(c, "env") {
				for _, r := range []struct{ key, kind string }{{"configMapKeyRef", "configmaps"}, {"secretKeyRef", "secrets"}} {
					if m, ok := fieldAt(e, "valueFrom", r.key).(map[string]any); ok {
						add(r.kind, m, "name", fmt.Sprintf("%s.env[%d].valueFrom.%s", base, j, r.key), true)
					}
				}
			}
			for j, e := range slice(c, "envFrom") {
				for _, r := range []struct{ key, kind string }{{"configMapRef", "configmaps"}, {"secretRef", "secrets"}} {
					if m, ok := fieldAt(e, r.key).(map[string]any); ok {
						add(r.kind, m, "name", fmt.Sprintf("%s.envFrom[%d].%s", base, j, r.key), false)
					}
				}
			}
		}
	}
	for i, v := range slice(spec, "volumes") {
		for _, r := range []struct{ key, kind, name string }{{"configMap", "configmaps", "name"}, {"secret", "secrets", "secretName"}, {"persistentVolumeClaim", "persistentvolumeclaims", "claimName"}} {
			if m, ok := fieldAt(v, r.key).(map[string]any); ok {
				add(r.kind, m, r.name, fmt.Sprintf("volumes[%d].%s", i, r.key), len(slice(m, "items")) > 0)
			}
		}
		for j, p := range slice(v, "projected", "sources") {
			for _, r := range []struct{ key, kind string }{{"configMap", "configmaps"}, {"secret", "secrets"}} {
				if m, ok := fieldAt(p, r.key).(map[string]any); ok {
					add(r.kind, m, "name", fmt.Sprintf("volumes[%d].projected.sources[%d].%s", i, j, r.key), len(slice(m, "items")) > 0)
				}
			}
		}
	}
	for i, p := range slice(spec, "imagePullSecrets") {
		add("secrets", p, "name", fmt.Sprintf("imagePullSecrets[%d]", i), false)
	}
	state := "ready"
	for _, r := range refs {
		if d.referencesRead >= 32 {
			state = "partial"
			break
		}
		d.referencesRead++
		coverage := ref.String() + "/" + r.field
		if d.s.graphMetadata == nil {
			d.cover(coverage, "unavailable")
			continue
		}
		m, err := d.s.graphMetadata.Resource(schema.GroupVersionResource{Version: "v1", Resource: r.kind}).Namespace(ref.Scope).Get(d.ctx, r.name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				reason := "RequiredReferenceMissing"
				health := core.HealthWarning
				if r.optional {
					reason = "OptionalReferenceAbsent"
					health = core.HealthOK
				}
				d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "reference", Field: r.field, State: health, Reason: reason, Message: r.kind + "/" + r.name})
				d.cover(coverage, "ready")
			} else {
				class, _ := classify(err)
				d.cover(coverage, string(class))
			}
			continue
		}
		if m.GetUID() == "" || m.GetNamespace() != ref.Scope || m.GetName() != r.name {
			d.cover(coverage, "unknown")
			continue
		}
		related := core.Ref{Provider: ProviderID, Target: d.s.target, Scope: ref.Scope, Kind: r.kind, Name: r.name, UID: string(m.GetUID())}
		d.evidence(core.DiagnosisEvidence{Ref: ref, Source: ref, Rule: "reference", Field: r.field, State: core.HealthUnknown, Reason: "ReferenceExists", Related: &related})
		if r.key {
			d.cover(coverage, "keys_unchecked")
		} else {
			d.cover(coverage, "ready")
		}
	}
	d.cover(ref.String()+"/references", state)
}
