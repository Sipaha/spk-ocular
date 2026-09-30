package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	// A node is taken out of scheduling (cordon), back in (uncordon).
	actCordon   = core.ActionDescriptor{ID: "cordon", Title: "Cordon"}
	actUncordon = core.ActionDescriptor{ID: "uncordon", Title: "Uncordon"}
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
	nodesKind:        {actCordon, actUncordon, actDrain},
}

func (d *kindDef) action(id string) (core.ActionDescriptor, bool) {
	acts := kindActions[d]
	if d.discovered {
		acts = d.actions
	}
	for _, a := range acts {
		if a.ID == id {
			return a, true
		}
	}
	return core.ActionDescriptor{}, false
}

var _ provider.Actioner = (*session)(nil)

// actionTarget resolves and checks what an action names.
func (s *session) actionTarget(ref core.Ref, action string, p core.ActionParams, final bool) (*kindDef, core.ActionDescriptor, error) {
	def := s.kind(ref.Kind)
	if def == nil {
		return nil, core.ActionDescriptor{}, provider.Said(provider.ClassUnsupported, msg("error.unknownKind", "kind", strconv.Quote(ref.Kind)))
	}
	d, ok := def.action(action)
	if !ok {
		return nil, core.ActionDescriptor{}, provider.Said(provider.ClassUnsupported, msg("error.noAction", "kind", def.desc.Title, "action", action))
	}
	if err := d.CheckParams(p, final); err != nil {
		// The API checked the same descriptor first: not reached from the UI.
		return nil, core.ActionDescriptor{}, &provider.Error{Class: provider.ClassInvalid, Message: err.Error()}
	}
	return def, d, nil
}

// actionExpect fingerprints the route the plan was read through (the
// resource's version and scope: a discovered kind may move between them)
// and the action, its parameters and the state of u the effects depend on
// (status churn is not part of it): "<route>-<state>".
func actionExpect(def *kindDef, action string, p core.ActionParams, u *unstructured.Unstructured) string {
	b, _ := json.Marshal(map[string]any{"action": action, "params": p, "state": effectState(def, action, u)})
	sum := sha256.Sum256(b)
	return routeOf(def) + "-" + hex.EncodeToString(sum[:12])
}

// routeOf fingerprints where a kind's objects are read and written.
func routeOf(def *kindDef) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%t", def.gvr, def.namespaced)))
	return hex.EncodeToString(sum[:4])
}

// sameRoute: the plan (its Expect) was read through def's route.
func sameRoute(def *kindDef, expect string) bool {
	r, _, ok := strings.Cut(expect, "-")
	return ok && r == routeOf(def)
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
	case actCordon.ID, actUncordon.ID:
		st["unschedulable"] = boolAt(o, "spec", "unschedulable")
	case actSuspend.ID, actResume.ID:
		cronJobEffectState(action, o, st)
	case actDelete.ID:
		if sts {
			st["claims"], st["whenDeleted"] = len(slice(o, "spec", "volumeClaimTemplates")), str(o, "spec", "persistentVolumeClaimRetentionPolicy", "whenDeleted")
		}
		// The deletion waits for them (the plan says which).
		fin := append([]string{}, u.GetFinalizers()...)
		sort.Strings(fin)
		st["finalizers"] = fin
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

// actionUnavailable: why action cannot run on u in its state (nil — it can).
func actionUnavailable(def *kindDef, action string, u *unstructured.Unstructured) *core.Message {
	if u.GetDeletionTimestamp() != nil {
		m := msg("unavailable.deleting", "kind", singular(def), "name", u.GetName())
		return &m
	}
	if action == actRestart.ID && def == deploymentsKind && boolAt(u.Object, "spec", "paused") {
		m := msg("unavailable.paused", "name", u.GetName())
		return &m
	}
	cordoned := boolAt(u.Object, "spec", "unschedulable")
	if action == actCordon.ID && cordoned {
		m := msg("unavailable.cordoned", "name", u.GetName())
		return &m
	}
	if action == actUncordon.ID && !cordoned {
		m := msg("unavailable.schedulable", "name", u.GetName())
		return &m
	}
	if isCronJobs(def) {
		return cronJobUnavailable(action, u)
	}
	return nil
}

// singular: "deployment", as in sentences.
func singular(def *kindDef) string {
	if def.discovered {
		return strings.ToLower(def.desc.Singular)
	}
	return strings.ToLower(kindSingular[def])
}

// getConfirmed reads the confirmed object through the run's route: its
// UID is required and must match; a missing object is gone as well (the
// confirmation was of it).
func (s *session) getConfirmed(ctx context.Context, def *kindDef, ref core.Ref) (*unstructured.Unstructured, error) {
	if ref.UID == "" {
		return nil, provider.Said(provider.ClassInvalid, msg("error.noUID"))
	}
	u, err := s.getObjectOf(ctx, def, ref)
	var pe *provider.Error
	if errors.As(err, &pe) && pe.Class == provider.ClassNotFound {
		return nil, provider.Said(provider.ClassGone, msg("error.gone", "kind", ref.Kind, "name", ref.Name))
	}
	return u, err
}

func (s *session) RunAction(ctx context.Context, run provider.ActionRun) (core.ActionResult, error) {
	def, _, err := s.actionTarget(run.Ref, run.Action, run.Params, true)
	if err != nil {
		return core.ActionResult{}, err
	}
	switch run.Action {
	case actDrain.ID:
		return s.runDrain(ctx, def, run)
	case actRunNow.ID:
		return s.runNow(ctx, def, run)
	}
	if !sameRoute(def, run.Expect) {
		// Reviewed through another version or scope of the resource: never
		// read or written through the current one instead. Checked, the
		// route holds for the whole run (reads, write, retries) even if the
		// catalog moves meanwhile.
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.routeChanged", "kind", def.desc.Title))
	}
	for attempt := 0; ; attempt++ {
		u, err := s.getConfirmed(ctx, def, run.Ref)
		if err != nil {
			return core.ActionResult{}, err
		}
		if why := actionUnavailable(def, run.Action, u); why != nil {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
		}
		if actionExpect(def, run.Action, run.Params, u) != run.Expect {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.changed", "kind", singular(def), "name", u.GetName()))
		}
		if s.beforeWrite != nil {
			s.beforeWrite(run.Action, u)
		}
		said, err := s.write(ctx, def, run, u)
		if err == nil {
			return core.ActionResult{Message: said}, nil
		}
		retry, err := s.failedWrite(ctx, def, run, u, err)
		if !retry || attempt == maxVersionRetries {
			return core.ActionResult{}, err
		}
	}
}

// write performs the action on u with u's UID and version as
// preconditions.
func (s *session) write(ctx context.Context, def *kindDef, run provider.ActionRun, u *unstructured.Unstructured) (core.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	ns := u.GetNamespace()
	kind, name := singular(def), u.GetName()
	switch run.Action {
	case actRestart.ID:
		patch, _ := json.Marshal(map[string]any{
			"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
			// Nanoseconds: two restarts within a second are two rollouts.
			"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{restartedAtKey: time.Now().Format(time.RFC3339Nano)}}}},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.MergePatchType, patch, "")
		return msg("done.restart", "kind", kind, "name", name), err
	case actScale.ID:
		// The count shown was checked by Expect on this read; its version
		// pins it: a stale version or another UID is a 409 (kind). Not a
		// JSON Patch test: its failure is a bare 422, indistinguishable
		// from a validation refusal.
		m := *run.Params.Count
		patch, _ := json.Marshal(map[string]any{
			"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
			"spec":     map[string]any{"replicas": m},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.MergePatchType, patch, "scale")
		return msg("done.scale", "kind", kind, "name", name, "from", replicas(u.Object), "to", m), err
	case actCordon.ID, actUncordon.ID:
		patch, _ := json.Marshal(map[string]any{
			"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
			"spec":     map[string]any{"unschedulable": run.Action == actCordon.ID},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.MergePatchType, patch, "")
		return msg("done."+run.Action, "kind", kind, "name", name), err
	case actSuspend.ID, actResume.ID:
		patch, _ := json.Marshal(map[string]any{
			"metadata": map[string]any{"uid": u.GetUID(), "resourceVersion": u.GetResourceVersion()},
			"spec":     map[string]any{"suspend": run.Action == actSuspend.ID},
		})
		err := wr.patch(ctx, def.gvr, ns, u.GetName(), types.MergePatchType, patch, "")
		return msg("done."+run.Action, "kind", kind, "name", name), err
	case actDelete.ID:
		uid, rv, bg := u.GetUID(), u.GetResourceVersion(), metav1.DeletePropagationBackground
		err := wr.delete(ctx, def.gvr, ns, u.GetName(), metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &rv}, PropagationPolicy: &bg})
		return msg("done.delete", "kind", kind, "name", name), err
	}
	return core.Message{}, &provider.Error{Class: provider.ClassUnsupported, Message: run.Action}
}

// failedWrite classifies a failed write; retry: nothing was written and
// only the object's version changed (re-read, same UID and Expect).
func (s *session) failedWrite(ctx context.Context, def *kindDef, run provider.ActionRun, was *unstructured.Unstructured, err error) (bool, error) {
	var se apierrors.APIStatus
	switch {
	case !errors.As(err, &se) && strings.Contains(err.Error(), "getting credentials"): // before sending
		class, msg := classify(err)
		return false, &provider.Error{Class: class, Message: msg}
	case !errors.As(err, &se):
		// No answer from the API server: it may or may not have applied it.
		return false, provider.Said(provider.ClassUnknown, msg("error.unknown", "detail", err.Error(), "kind", singular(def), "name", was.GetName()))
	case apierrors.IsForbidden(err):
		return false, &provider.Error{Class: provider.ClassForbidden, Message: statusMessage(err)}
	case apierrors.IsNotFound(err):
		return false, provider.Said(provider.ClassGone, msg("error.gone", "kind", singular(def), "name", was.GetName()))
	case ambiguous(err):
		// A server or proxy failure answer: the write may have been applied.
		return false, provider.Said(provider.ClassUnknown, msg("error.unknown", "detail", statusMessage(err), "kind", singular(def), "name", was.GetName()))
	case !apierrors.IsConflict(err) && !apierrors.IsInvalid(err):
		class, msg := classify(err)
		return false, &provider.Error{Class: class, Message: msg}
	}
	// 409 (a failed precondition) or 422 (an immutable UID, validation,
	// admission): look again to tell which.
	now, gerr := s.getConfirmed(ctx, def, run.Ref)
	switch {
	case gerr != nil:
		var pe *provider.Error
		if errors.As(gerr, &pe) && pe.Class == provider.ClassGone {
			return false, provider.Said(provider.ClassGone, msg("error.replacedMeanwhile", "kind", singular(def), "name", was.GetName()))
		}
		return false, gerr
	case actionExpect(def, run.Action, run.Params, now) != run.Expect:
		return false, provider.Said(provider.ClassConflict, msg("error.changed", "kind", singular(def), "name", was.GetName()))
	case now.GetResourceVersion() != was.GetResourceVersion() && apierrors.IsConflict(err):
		// A failed precondition at a moved version: retry. Other refusals
		// stay refusals.
		return true, provider.Said(provider.ClassConflict, msg("error.keepsChanging", "kind", singular(def), "name", was.GetName()))
	case apierrors.IsConflict(err):
		return false, &provider.Error{Class: provider.ClassConflict, Message: statusMessage(err)}
	}
	return false, &provider.Error{Class: provider.ClassInvalid, Message: statusMessage(err)}
}

// prepareExtrasTimeout bounds the optional parts of a plan (rights,
// autoscalers, pod counts): late ones are reported as unknown.
var prepareExtrasTimeout = 5 * time.Second // a variable for tests

// PrepareAction reads what the action would do; nothing changes.
func (s *session) PrepareAction(ctx context.Context, ref core.Ref, action string, p core.ActionParams) (core.ActionPlan, error) {
	def, d, err := s.actionTarget(ref, action, p, false)
	if err != nil {
		return core.ActionPlan{}, err
	}
	u, err := s.getObjectOf(ctx, def, ref) // an unpinned ref (from a relation) is pinned here
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
	if action == actDrain.ID {
		return s.prepareDrain(ctx, def, plan, u), nil
	}
	if action == actScale.ID {
		n := replicas(u.Object)
		plan.Current = &n
	}
	fx := effects(def, action, p, u)
	plan.Effects, plan.Warnings = fx.effects, fx.warnings
	plan.Destructive = plan.Destructive || fx.destructive
	if action == actRunNow.ID {
		plan = s.prepareRunNow(def, plan, u)
	}

	// The optional parts, in parallel within one deadline; an answer that
	// ignores it (a stuck transport) is not waited for.
	ectx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	rights := make(chan core.Rights, 1)
	hpas := make(chan []core.Message, 1)
	podCount := make(chan []core.Message, 1)
	go func() { rights <- s.rights(ectx, def, action, u) }()
	waitHPAs := action == actScale.ID
	if waitHPAs {
		go func() { hpas <- s.autoscalers(ectx, def, u) }()
	}
	waitPods := action == actDelete.ID && def != podsKind && def.gvr.Group == "apps"
	if waitPods {
		go func() { podCount <- s.podsOf(ectx, def, u) }()
	}
	waitRights := true
	var podFx []core.Message
wait:
	for waitRights || waitHPAs || waitPods {
		select {
		case plan.Rights = <-rights:
			waitRights = false
		case w := <-hpas:
			plan.Warnings = append(plan.Warnings, w...)
			waitHPAs = false
		case podFx = <-podCount:
			waitPods = false
		case <-ectx.Done():
			break wait
		}
	}
	if waitRights {
		plan.Rights = core.Rights{State: core.RightsUnknown, Reason: "the check took too long"}
	}
	if waitHPAs {
		plan.Warnings = append(plan.Warnings, msg("hpa.checkLate"))
	}
	plan.Effects = append(plan.Effects, podFx...) // a late pod count is left out
	if def == podsKind && action == actDelete.ID {
		plan.Effects = append(plan.Effects, s.podController(ctx, u))
	}
	return plan, nil
}
