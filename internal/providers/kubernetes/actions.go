package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Actions (P4): a plan is read first (PrepareAction), the run re-reads the
// object by its UID, checks the plan's Expect — the action, its parameters
// and the state the shown effects depend on — and writes with the object's
// UID and resourceVersion as preconditions. A failed version precondition
// is retried only when the object is proven unchanged but for its version
// (nothing was written); nothing else is ever retried.

const restartedAtKey = "kubectl.kubernetes.io/restartedAt"

// maxVersionRetries bounds retries after a version-only change.
const maxVersionRetries = 3

// maxReplicas: more is almost certainly a typo.
const maxReplicas = 10000

var (
	actRestart = core.ActionDescriptor{ID: "restart", Title: "Restart"}
	actScale   = core.ActionDescriptor{ID: "scale", Title: "Scale", Param: &core.ActionParam{Kind: core.ParamCount, Min: 0, Max: maxReplicas}}
	actDelete  = core.ActionDescriptor{ID: "delete", Title: "Delete", Destructive: true}
)

// kindActions: namespaces and nodes are not deleted here (the blast radius
// of a namespace, a node is drained rather than deleted); events expire.
var kindActions = map[*kindDef][]core.ActionDescriptor{
	deploymentsKind:  {actRestart, actScale, actDelete},
	statefulSetsKind: {actRestart, actScale, actDelete},
	daemonSetsKind:   {actRestart, actDelete},
	replicaSetsKind:  {actDelete},
	podsKind:         {actDelete},
	servicesKind:     {actDelete},
	ingressesKind:    {actDelete},
	configMapsKind:   {actDelete},
	secretsKind:      {actDelete},
}

func (d *kindDef) action(id string) (core.ActionDescriptor, bool) {
	for _, a := range kindActions[d] {
		if a.ID == id {
			return a, true
		}
	}
	return core.ActionDescriptor{}, false
}

var _ provider.Actioner = (*session)(nil)

// actionTarget resolves and checks what an action names.
func (s *session) actionTarget(ref core.Ref, action string, p core.ActionParams, final bool) (*kindDef, core.ActionDescriptor, error) {
	def := s.kinds.byID[ref.Kind]
	if def == nil {
		return nil, core.ActionDescriptor{}, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("unknown kind %q", ref.Kind)}
	}
	d, ok := def.action(action)
	if !ok {
		return nil, core.ActionDescriptor{}, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s cannot be %s", def.desc.Title, action)}
	}
	if err := d.CheckParams(p, final); err != nil {
		return nil, core.ActionDescriptor{}, invalid("%s", err.Error())
	}
	return def, d, nil
}

// actionExpect fingerprints the action, its parameters and the state of u
// the effects depend on (status churn is not part of it).
func actionExpect(def *kindDef, action string, p core.ActionParams, u *unstructured.Unstructured) string {
	b, _ := json.Marshal(map[string]any{"action": action, "params": p, "state": effectState(def, action, u)})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

func effectState(def *kindDef, action string, u *unstructured.Unstructured) map[string]any {
	o := u.Object
	st := map[string]any{"uid": string(u.GetUID()), "deleting": u.GetDeletionTimestamp() != nil}
	sts := def == statefulSetsKind
	switch action {
	case actRestart.ID:
		st["replicas"], st["paused"] = replicas(o), boolAt(o, "spec", "paused")
		if def == deploymentsKind {
			st["strategy"] = fieldAt(o, "spec", "strategy")
		} else {
			st["strategy"] = fieldAt(o, "spec", "updateStrategy")
		}
	case actScale.ID:
		st["replicas"] = replicas(o)
		if sts {
			// Everything the scale effects read: both policies (claims kept at
			// scale down go with a whenDeleted=Delete StatefulSet) and the
			// first ordinal (the named pods).
			st["claims"], st["whenScaled"] = len(slice(o, "spec", "volumeClaimTemplates")), str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenScaled")
			st["whenDeleted"], st["start"] = str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted"), i64(o, "spec", "ordinals", "start")
		}
	case actDelete.ID:
		if sts {
			st["claims"], st["whenDeleted"] = len(slice(o, "spec", "volumeClaimTemplates")), str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted")
		}
		if c := metav1.GetControllerOf(u); c != nil {
			st["controller"] = c.Kind + "/" + c.Name + "/" + string(c.UID)
		}
	}
	return st
}

func fieldAt(o map[string]any, path ...string) any {
	v, _, _ := unstructured.NestedFieldNoCopy(o, path...)
	return v
}

func boolAt(o map[string]any, path ...string) bool {
	v, _, _ := unstructured.NestedBool(o, path...)
	return v
}

// replicas: spec.replicas (defaulted to 1 by the API server).
func replicas(o map[string]any) int {
	if v, ok := fieldAt(o, "spec", "replicas").(int64); ok {
		return int(v)
	}
	if v, ok := fieldAt(o, "spec", "replicas").(float64); ok {
		return int(v)
	}
	return 1
}

// actionUnavailable: why action cannot run on u in its state ("" — it can).
func actionUnavailable(def *kindDef, action string, u *unstructured.Unstructured) string {
	if u.GetDeletionTimestamp() != nil {
		return fmt.Sprintf("%s %s is being deleted", singular(def), u.GetName())
	}
	if action == actRestart.ID && def == deploymentsKind && boolAt(u.Object, "spec", "paused") {
		return fmt.Sprintf("deployment %s is paused: resume its rollout first", u.GetName())
	}
	return ""
}

func singular(def *kindDef) string {
	return strings.ToLower(strings.TrimSuffix(def.desc.Title, "s"))
}

// getConfirmed reads the confirmed object: its UID is required and must
// match; a missing object is gone as well (the confirmation was of it).
func (s *session) getConfirmed(ctx context.Context, ref core.Ref) (*unstructured.Unstructured, error) {
	if ref.UID == "" {
		return nil, invalid("the object's UID is missing: act only on a confirmed object")
	}
	u, err := s.getObject(ctx, ref)
	var pe *provider.Error
	if errors.As(err, &pe) && pe.Class == provider.ClassNotFound {
		return nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s %s no longer exists", ref.Kind, ref.Name)}
	}
	return u, err
}

func (s *session) RunAction(ctx context.Context, run provider.ActionRun) (core.ActionResult, error) {
	def, _, err := s.actionTarget(run.Ref, run.Action, run.Params, true)
	if err != nil {
		return core.ActionResult{}, err
	}
	for attempt := 0; ; attempt++ {
		u, err := s.getConfirmed(ctx, run.Ref)
		if err != nil {
			return core.ActionResult{}, err
		}
		if why := actionUnavailable(def, run.Action, u); why != "" {
			return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: why}
		}
		if actionExpect(def, run.Action, run.Params, u) != run.Expect {
			return core.ActionResult{}, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("%s %s changed since the action was reviewed; review it again", singular(def), u.GetName())}
		}
		if s.beforeWrite != nil {
			s.beforeWrite(run.Action, u)
		}
		msg, err := s.write(ctx, def, run, u)
		if err == nil {
			return core.ActionResult{Message: msg}, nil
		}
		retry, err := s.failedWrite(ctx, def, run, u, err)
		if !retry || attempt == maxVersionRetries {
			return core.ActionResult{}, err
		}
	}
}

// write performs the action on u with u's UID and version as
// preconditions.
func (s *session) write(ctx context.Context, def *kindDef, run provider.ActionRun, u *unstructured.Unstructured) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	ns := u.GetNamespace()
	name := fmt.Sprintf("%s %s", singular(def), u.GetName())
	switch run.Action {
	case actRestart.ID:
		patch, _ := json.Marshal(map[string]any{
			"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
			// Nanoseconds: two restarts within a second are two rollouts.
			"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{restartedAtKey: time.Now().Format(time.RFC3339Nano)}}}},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.MergePatchType, patch, "")
		return name + ": restart requested", err
	case actScale.ID:
		m := *run.Params.Count
		patch, _ := json.Marshal([]map[string]any{
			{"op": "test", "path": "/metadata/uid", "value": u.GetUID()},
			{"op": "test", "path": "/metadata/resourceVersion", "value": u.GetResourceVersion()},
			{"op": "test", "path": "/spec/replicas", "value": replicas(u.Object)},
			{"op": "replace", "path": "/spec/replicas", "value": m},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.JSONPatchType, patch, "scale")
		return fmt.Sprintf("%s: scale %d → %d requested", name, replicas(u.Object), m), err
	case actDelete.ID:
		uid, rv, bg := u.GetUID(), u.GetResourceVersion(), metav1.DeletePropagationBackground
		err := wr.delete(ctx, def.gvr, ns, u.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &bg})
		return name + ": deletion requested", err
	}
	return "", &provider.Error{Class: provider.ClassUnsupported, Message: run.Action}
}

// failedWrite classifies a failed write; retry: nothing was written and
// only the object's version changed (re-read, same UID and Expect).
func (s *session) failedWrite(ctx context.Context, def *kindDef, run provider.ActionRun, was *unstructured.Unstructured, err error) (bool, error) {
	var se apierrors.APIStatus
	switch {
	case strings.Contains(err.Error(), "getting credentials"): // before sending
		class, msg := classify(err)
		return false, &provider.Error{Class: class, Message: msg}
	case !errors.As(err, &se):
		// No answer from the API server: it may or may not have applied it.
		return false, &provider.Error{Class: provider.ClassUnknown, Message: fmt.Sprintf("the result is not known (%v): check %s %s before repeating", err, singular(def), was.GetName())}
	case apierrors.IsForbidden(err):
		return false, &provider.Error{Class: provider.ClassForbidden, Message: statusMessage(err)}
	case apierrors.IsNotFound(err):
		return false, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s %s no longer exists", singular(def), was.GetName())}
	case ambiguous(err):
		// A server or proxy failure answer: the write may have been applied.
		return false, &provider.Error{Class: provider.ClassUnknown, Message: fmt.Sprintf("the result is not known (%s): check %s %s before repeating", statusMessage(err), singular(def), was.GetName())}
	case !apierrors.IsConflict(err) && !apierrors.IsInvalid(err):
		class, msg := classify(err)
		return false, &provider.Error{Class: class, Message: msg}
	}
	// 409 (a precondition) or 422 (a failed JSON Patch test, an immutable
	// UID, or plain validation): look again to tell which.
	now, gerr := s.getConfirmed(ctx, run.Ref)
	switch {
	case gerr != nil:
		var pe *provider.Error
		if errors.As(gerr, &pe) && pe.Class == provider.ClassGone {
			return false, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s %s was deleted or replaced by a new one with the same name", singular(def), was.GetName())}
		}
		return false, gerr
	case actionExpect(def, run.Action, run.Params, now) != run.Expect:
		return false, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("%s %s changed since the action was reviewed; review it again", singular(def), was.GetName())}
	case now.GetResourceVersion() != was.GetResourceVersion() && (apierrors.IsConflict(err) || run.Action == actScale.ID):
		// A failed precondition (409; a scale's failed JSON Patch test is
		// 422) at a moved version: retry. Other refusals stay refusals.
		return true, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("%s %s keeps changing; try again", singular(def), was.GetName())}
	case apierrors.IsConflict(err):
		return false, &provider.Error{Class: provider.ClassConflict, Message: statusMessage(err)}
	}
	return false, &provider.Error{Class: provider.ClassInvalid, Message: statusMessage(err)}
}

// prepareExtrasTimeout bounds the optional parts of a plan (rights,
// autoscalers, pod counts): late ones are reported as unknown.
const prepareExtrasTimeout = 5 * time.Second

// PrepareAction reads what the action would do; nothing changes.
func (s *session) PrepareAction(ctx context.Context, ref core.Ref, action string, p core.ActionParams) (core.ActionPlan, error) {
	def, d, err := s.actionTarget(ref, action, p, false)
	if err != nil {
		return core.ActionPlan{}, err
	}
	u, err := s.getObject(ctx, ref) // an unpinned ref (from a relation) is pinned here
	if err != nil {
		return core.ActionPlan{}, err
	}
	ref.UID, ref.Scope, ref.Name = string(u.GetUID()), u.GetNamespace(), u.GetName()
	plan := core.ActionPlan{
		Where: core.LiveTarget{Provider: ProviderID, Target: s.conn.target, TargetTitle: s.conn.targetTitle, Endpoint: s.conn.endpoint,
			ConfigHash: s.hash, Ref: ref},
		Action: d, Params: p, Destructive: d.Destructive,
		Unavailable: actionUnavailable(def, action, u),
		Expect:      actionExpect(def, action, p, u),
	}
	if action == actScale.ID {
		n := replicas(u.Object)
		plan.Current = &n
	}
	fx := effects(def, action, p, u)
	plan.Effects, plan.Warnings = fx.effects, fx.warnings
	plan.Destructive = plan.Destructive || fx.destructive

	// The optional parts, in parallel within one deadline.
	ectx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	type extra struct {
		effects, warnings []string
	}
	rights := make(chan core.Rights, 1)
	extras := make(chan extra, 2)
	go func() { rights <- s.rights(ectx, def, action, u) }()
	pending := 0
	if action == actScale.ID {
		pending++
		go func() { w := s.autoscalers(ectx, def, u); extras <- extra{warnings: w} }()
	}
	if action == actDelete.ID && def != podsKind && def.gvr.Group == "apps" {
		pending++
		go func() { extras <- extra{effects: s.podsOf(ectx, def, u)} }()
	}
	plan.Rights = <-rights
	for ; pending > 0; pending-- {
		e := <-extras
		plan.Effects = append(plan.Effects, e.effects...)
		plan.Warnings = append(plan.Warnings, e.warnings...)
	}
	if def == podsKind && action == actDelete.ID {
		plan.Effects = append(plan.Effects, s.podController(ctx, u))
	}
	return plan, nil
}
