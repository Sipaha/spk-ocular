package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Drain (P11): cordon the node, then ask the API server to evict its pods
// one at a time (the Eviction API: PodDisruptionBudgets are consulted by
// the server at each eviction). Unlike kubectl drain: finished and deleting
// pods are left alone, pods without a controller are left and named rather
// than the whole drain refused, nothing waits for the pods to go.
//
// The plan binds (Expect) the node and a fingerprint of every pod it
// writes or names: identity, class, controller, emptyDir volumes. A run
// lists the pods again and writes nothing unless they match.

var actDrain = core.ActionDescriptor{ID: "drain", Title: "Drain", Destructive: true}

const (
	// maxDrainPods: a node's pods one drain may know (kubelet's default
	// is 110); more — or a list cut short — and the drain is unavailable.
	maxDrainPods = 500
	// drainParallel bounds the rights and PodDisruptionBudget reads.
	drainParallel = 8
	// PodDisruptionBudgets are forecast for so many namespaces, so many
	// budgets in each; beyond, they are said to be unchecked.
	maxPDBNamespaces    = 50
	maxPDBsPerNamespace = 200
)

var pdbGVR = schema.GroupVersionResource{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}

// drainClass: what a drain does with a pod.
type drainClass string

const (
	drainEvict    drainClass = "evict"
	drainBare     drainClass = "bare" // no controller: left on the node, named
	drainDaemon   drainClass = "daemon"
	drainMirror   drainClass = "mirror"
	drainFinished drainClass = "finished"
	drainDeleting drainClass = "deleting"
)

const mirrorAnnotation = "kubernetes.io/config.mirror"

type drainPod struct {
	u         *unstructured.Unstructured
	class     drainClass
	owner     *metav1.OwnerReference // the controller
	emptyDirs []string
}

func (p drainPod) name() string { return p.u.GetNamespace() + "/" + p.u.GetName() }

func classifyPod(u *unstructured.Unstructured) drainPod {
	p := drainPod{u: u, owner: metav1.GetControllerOf(u)}
	for _, v := range slice(u.Object, "spec", "volumes") {
		if _, ok := v["emptyDir"]; ok {
			p.emptyDirs = append(p.emptyDirs, strOf(v, "name"))
		}
	}
	sort.Strings(p.emptyDirs)
	phase := str(u.Object, "status", "phase")
	switch {
	case u.GetDeletionTimestamp() != nil:
		p.class = drainDeleting
	case phase == "Succeeded" || phase == "Failed":
		p.class = drainFinished
	case u.GetAnnotations()[mirrorAnnotation] != "":
		p.class = drainMirror
	case p.owner != nil && p.owner.Kind == "DaemonSet":
		p.class = drainDaemon
	case p.owner == nil:
		p.class = drainBare
	default:
		p.class = drainEvict
	}
	return p
}

// print is what the plan promises about p: a change of any of it (a
// controller removed or replaced, another class) needs a new review.
// Status churn (versions, conditions) is not part of it.
func (p drainPod) print() map[string]any {
	out := map[string]any{"ns": p.u.GetNamespace(), "name": p.u.GetName(), "uid": string(p.u.GetUID()),
		"node": str(p.u.Object, "spec", "nodeName"), "class": p.class, "emptyDir": p.emptyDirs}
	if p.owner != nil {
		out["owner"] = []string{p.owner.APIVersion, p.owner.Kind, p.owner.Name, string(p.owner.UID)}
	}
	return out
}

// drainInventory: a node's pods by class, each sorted by namespace/name.
type drainInventory struct {
	evict, bare, left []drainPod
}

// drainPods lists node's pods (all namespaces, one bounded request); why:
// the drain cannot know them all (no right, an error, too many).
func (s *session) drainPods(ctx context.Context, node string) (*drainInventory, *core.Message) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	l, err := s.dyn.Resource(podsKind.gvr).List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node, Limit: maxDrainPods + 1})
	if err != nil {
		m := msg("drain.podsUnreadable", "reason", shortErr(err))
		return nil, &m
	}
	if l.GetContinue() != "" || len(l.Items) > maxDrainPods {
		m := msg("drain.tooMany", "max", maxDrainPods)
		return nil, &m
	}
	inv := &drainInventory{}
	for i := range l.Items {
		u := &l.Items[i]
		if str(u.Object, "spec", "nodeName") != node { // a server ignoring the field selector
			continue
		}
		switch p := classifyPod(u); p.class {
		case drainEvict:
			inv.evict = append(inv.evict, p)
		case drainBare:
			inv.bare = append(inv.bare, p)
		default:
			inv.left = append(inv.left, p)
		}
	}
	for _, ps := range [][]drainPod{inv.evict, inv.bare, inv.left} {
		sort.Slice(ps, func(i, j int) bool { return ps[i].name() < ps[j].name() })
	}
	return inv, nil
}

// drainExpect: the route, the node (UID, cordoned) and the fingerprints of
// the pods the drain evicts or names.
func drainExpect(def *kindDef, node *unstructured.Unstructured, inv *drainInventory) string {
	prints := []map[string]any{}
	for _, ps := range [][]drainPod{inv.evict, inv.bare} {
		for _, p := range ps {
			prints = append(prints, p.print())
		}
	}
	b, _ := json.Marshal(map[string]any{"action": actDrain.ID, "uid": string(node.GetUID()), "unschedulable": boolAt(node.Object, "spec", "unschedulable"), "pods": prints})
	sum := sha256.Sum256(b)
	return routeOf(def) + "-" + hex.EncodeToString(sum[:12])
}

// prepareDrain reads what a drain of node u would do; nothing changes.
func (s *session) prepareDrain(ctx context.Context, def *kindDef, plan core.ActionPlan, u *unstructured.Unstructured) core.ActionPlan {
	plan.Destructive = true
	if why := actionUnavailable(def, actDrain.ID, u); why != nil {
		plan.Unavailable, plan.Expect = why, routeOf(def)+"-unavailable"
		return plan
	}
	inv, why := s.drainPods(ctx, u.GetName())
	if why != nil {
		plan.Unavailable, plan.Expect = why, routeOf(def)+"-unavailable"
		return plan
	}
	plan.Expect = drainExpect(def, u, inv)
	cordon := !boolAt(u.Object, "spec", "unschedulable")
	if cordon {
		plan.Effects = append(plan.Effects, msg("node.cordon", "name", u.GetName()), msg("node.cordonBypass"))
	}
	switch {
	case len(inv.evict) > 0:
		plan.Effects = append(plan.Effects, countMsg("drain.evictOne", "drain.evict", len(inv.evict)), msg("drain.recreate"), msg("drain.noWait"))
	case cordon:
		plan.Effects = append(plan.Effects, msg("drain.onlyCordon"))
	default:
		m := msg("drain.nothing", "name", u.GetName())
		plan.Unavailable = &m
	}
	if len(inv.bare) > 0 {
		plan.Warnings = append(plan.Warnings, countMsg("drain.bareOne", "drain.bare", len(inv.bare)))
	}
	plan.Lists = drainLists(inv)
	if plan.Unavailable != nil {
		return plan
	}

	ectx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	tally := &rightsTally{}
	rights := make(chan struct{})
	pdbs := make(chan []core.Message, 1)
	go func() {
		s.drainRights(ectx, u, cordon, inv.evict, tally)
		close(rights)
	}()
	go func() { pdbs <- s.drainPDBs(ectx, inv.evict) }()
	waitRights, waitPDBs := true, true
	rightsDone := rights // nil once received: a closed channel is ready forever
wait:
	for waitRights || waitPDBs {
		select {
		case <-rightsDone:
			waitRights, rightsDone = false, nil
		case w := <-pdbs:
			plan.Warnings, waitPDBs = append(plan.Warnings, w...), false
		case <-ectx.Done():
			break wait
		}
	}
	select {
	case <-rights: // finished as the time ran out
		waitRights = false
	default:
	}
	// What is known by now: a refusal already answered stays one even when
	// a neighbouring check is late.
	r, denied := tally.result(!waitRights)
	plan.Rights = r
	if len(denied) > 0 {
		l := core.ActionList{Title: msg("drain.list.denied"), Destructive: true}
		for _, n := range denied {
			l.Items = append(l.Items, core.ActionItem{Name: n})
		}
		plan.Lists = append(plan.Lists, l)
	}
	if waitPDBs {
		plan.Warnings = append(plan.Warnings, msg("drain.pdbUnchecked"))
	}
	return plan
}

// drainLists: the pods evicted (their controllers), those losing emptyDir
// data, those left without a controller, and — collapsed — those a drain
// leaves alone and why.
func drainLists(inv *drainInventory) []core.ActionList {
	var out []core.ActionList
	if len(inv.evict) > 0 {
		l := core.ActionList{Title: msg("drain.list.evict"), Destructive: true}
		var lost core.ActionList
		for _, p := range inv.evict {
			n := msg("drain.item.owner", "ownerKind", p.owner.Kind, "owner", p.owner.Name)
			l.Items = append(l.Items, core.ActionItem{Name: p.name(), Note: &n})
			if len(p.emptyDirs) > 0 {
				v := msg("drain.item.volumes", "volumes", strings.Join(p.emptyDirs, ", "))
				lost.Items = append(lost.Items, core.ActionItem{Name: p.name(), Note: &v})
			}
		}
		out = append(out, l)
		if len(lost.Items) > 0 {
			lost.Title, lost.Destructive = msg("drain.list.emptyDir"), true
			out = append(out, lost)
		}
	}
	if len(inv.bare) > 0 {
		l := core.ActionList{Title: msg("drain.list.bare")}
		for _, p := range inv.bare {
			l.Items = append(l.Items, core.ActionItem{Name: p.name()})
		}
		out = append(out, l)
	}
	if len(inv.left) > 0 {
		l := core.ActionList{Title: msg("drain.list.left"), Collapsed: true}
		for _, p := range inv.left {
			n := msg("drain.skip." + string(p.class))
			l.Items = append(l.Items, core.ActionItem{Name: p.name(), Note: &n})
		}
		out = append(out, l)
	}
	return out
}

// access asks the API server (SelfSubjectAccessReview) about attrs: the
// state and, when denied, the authorizer's reason.
func (s *session) access(ctx context.Context, attrs map[string]any) (core.RightsState, string) {
	review := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview",
		"spec": map[string]any{"resourceAttributes": attrs},
	}}
	out, err := s.dyn.Resource(ssarGVR).Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return core.RightsUnknown, shortErr(err)
	}
	allowed, found, _ := unstructured.NestedBool(out.Object, "status", "allowed")
	switch {
	case !found:
		return core.RightsUnknown, ""
	case allowed:
		return core.RightsAllowed, ""
	}
	denied, _, _ := unstructured.NestedBool(out.Object, "status", "denied")
	if e := str(out.Object, "status", "evaluationError"); !denied && e != "" {
		return core.RightsUnknown, e
	}
	return core.RightsDenied, str(out.Object, "status", "reason")
}

// rightsTally gathers a drain's rights as their checks answer, so that
// what is known can be read at any moment (a deadline).
type rightsTally struct {
	mu         sync.Mutex
	denied     []string // pods
	nodeDenied string
	unknown    bool
}

func (t *rightsTally) note(st core.RightsState, pod string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch st {
	case core.RightsDenied:
		t.denied = append(t.denied, pod)
	case core.RightsUnknown:
		t.unknown = true
	}
}

// result: denied when any known check refused (with the pods refused),
// else unknown when any was unknown or not all answered (complete false).
func (t *rightsTally) result(complete bool) (core.Rights, []string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	denied := slices.Clone(t.denied)
	sort.Strings(denied)
	switch {
	case t.nodeDenied != "" || len(denied) > 0:
		reason := t.nodeDenied
		if len(denied) > 0 {
			if reason != "" {
				reason += "; "
			}
			reason += fmt.Sprintf("you may not evict %d of the pods", len(denied))
		}
		if !complete {
			reason += "; not all checked in time"
		}
		return core.Rights{State: core.RightsDenied, Reason: reason}, denied
	case !complete:
		return core.Rights{State: core.RightsUnknown, Reason: "the check took too long"}, nil
	case t.unknown:
		return core.Rights{State: core.RightsUnknown}, nil
	}
	return core.Rights{State: core.RightsAllowed}, nil
}

// drainRights checks only the writes of this plan: the node's patch when
// it is to be cordoned, and the eviction of each pod by name (a role may
// allow it for named pods only, so a namespace-wide refusal proves
// nothing; a namespace-wide allowance covers its pods). Answers go to t as
// they come.
func (s *session) drainRights(ctx context.Context, node *unstructured.Unstructured, cordon bool, evict []drainPod, t *rightsTally) {
	sem := make(chan struct{}, drainParallel)
	ask := func(attrs map[string]any) (core.RightsState, string) {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return core.RightsUnknown, "the check took too long"
		}
		defer func() { <-sem }()
		return s.access(ctx, attrs)
	}
	var wg sync.WaitGroup
	if cordon {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st, why := ask(map[string]any{"verb": "patch", "group": "", "resource": "nodes", "namespace": "", "name": node.GetName()})
			t.mu.Lock()
			defer t.mu.Unlock()
			switch st {
			case core.RightsDenied:
				t.nodeDenied = "you may not patch nodes"
				if why != "" {
					t.nodeDenied += " (" + why + ")"
				}
			case core.RightsUnknown:
				t.unknown = true
			}
		}()
	}
	byNS := map[string][]drainPod{}
	for _, p := range evict {
		byNS[p.u.GetNamespace()] = append(byNS[p.u.GetNamespace()], p)
	}
	for ns, ps := range byNS {
		wg.Add(1)
		go func() {
			defer wg.Done()
			eviction := func(name string) map[string]any {
				a := map[string]any{"verb": "create", "group": "", "resource": "pods", "subresource": "eviction", "namespace": ns}
				if name != "" {
					a["name"] = name
				}
				return a
			}
			if st, _ := ask(eviction("")); st == core.RightsAllowed {
				return
			}
			var pw sync.WaitGroup
			for _, p := range ps {
				pw.Add(1)
				go func() {
					defer pw.Done()
					st, _ := ask(eviction(p.u.GetName()))
					t.note(st, p.name())
				}()
			}
			pw.Wait()
		}()
	}
	wg.Wait()
}

// drainPDBs forecasts PodDisruptionBudgets of the pods to evict: those
// allowing no disruption now, pods under more than one (the server refuses
// those). A forecast only — the server decides at each eviction; budgets
// not read are said to be unchecked, never "none".
func (s *session) drainPDBs(ctx context.Context, evict []drainPod) []core.Message {
	byNS := map[string][]drainPod{}
	for _, p := range evict {
		byNS[p.u.GetNamespace()] = append(byNS[p.u.GetNamespace()], p)
	}
	namespaces := make([]string, 0, len(byNS))
	for ns := range byNS {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	unchecked := len(namespaces) > maxPDBNamespaces
	if unchecked {
		namespaces = namespaces[:maxPDBNamespaces]
	}
	type found struct {
		blocking map[string][]string // PDB → pods
		many     []string
		ok       bool
	}
	results := make([]found, len(namespaces))
	sem := make(chan struct{}, drainParallel)
	var wg sync.WaitGroup
	for i, ns := range namespaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()
			l, err := s.dyn.Resource(pdbGVR).Namespace(ns).List(ctx, metav1.ListOptions{Limit: maxPDBsPerNamespace + 1})
			if err != nil || l.GetContinue() != "" || len(l.Items) > maxPDBsPerNamespace {
				return
			}
			f := found{blocking: map[string][]string{}, ok: true}
			for _, p := range byNS[ns] {
				var matched []*unstructured.Unstructured
				for j := range l.Items {
					b := &l.Items[j]
					sel, ok, _ := unstructured.NestedMap(b.Object, "spec", "selector")
					if !ok || sel == nil { // a null selector selects no pods
						continue
					}
					var ls metav1.LabelSelector
					if err := runtimeFromMap(sel, &ls); err != nil {
						continue
					}
					s, err := metav1.LabelSelectorAsSelector(&ls)
					if err != nil || !s.Matches(labels.Set(p.u.GetLabels())) {
						continue
					}
					matched = append(matched, b)
				}
				if len(matched) > 1 {
					f.many = append(f.many, p.name())
				}
				for _, b := range matched {
					if i64(b.Object, "status", "disruptionsAllowed") == 0 {
						f.blocking[b.GetName()] = append(f.blocking[b.GetName()], p.name())
					}
				}
			}
			results[i] = f
		}()
	}
	wg.Wait()
	var out []core.Message
	for i, f := range results {
		if !f.ok {
			unchecked = true
			continue
		}
		names := make([]string, 0, len(f.blocking))
		for b := range f.blocking {
			names = append(names, b)
		}
		sort.Strings(names)
		for _, b := range names {
			out = append(out, countMsg("drain.pdbBlocksOne", "drain.pdbBlocks", len(f.blocking[b]), "pdb", namespaces[i]+"/"+b))
		}
		for _, p := range f.many {
			out = append(out, msg("drain.pdbMany", "pod", p))
		}
	}
	if unchecked {
		out = append(out, msg("drain.pdbUnchecked"))
	}
	return out
}

// runtimeFromMap converts an unstructured map into v (via JSON).
func runtimeFromMap(m map[string]any, v any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// drainRunTimeout bounds a whole drain run (reads, cordon, evictions) —
// under the dialog's 60 s; drainWriteTimeout bounds one write. Variables
// for tests.
var (
	drainRunTimeout   = 45 * time.Second
	drainWriteTimeout = 10 * time.Second
)

// disruptionBudgetCause: a 429 of an eviction for a PodDisruptionBudget
// (policy/v1 DisruptionBudgetCause), not throttling.
const disruptionBudgetCause = "DisruptionBudget"

// runDrain: the node and its pods are read again and must match the plan;
// then the cordon (if the node is open) and one eviction per pod, each one
// request. After the first write the answer is a result with parts, never
// an error — even when the time runs out or the caller goes.
func (s *session) runDrain(ctx context.Context, def *kindDef, run provider.ActionRun) (core.ActionResult, error) {
	if !sameRoute(def, run.Expect) {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.routeChanged", "kind", def.desc.Title))
	}
	ctx, cancel := context.WithTimeout(ctx, drainRunTimeout)
	defer cancel()
	var parts []core.ActionPart
	var inv *drainInventory
	var node *unstructured.Unstructured
	sent := false // a cordon went out: from here on a result with parts
	// stopped: a cordon sent and refused whose retry cannot go on (the
	// plan no longer holds, the node is gone, the time is up) — the
	// cordon's part refused, the pods last checked not started.
	stopped := func(why core.Message) {
		parts = append(parts, core.ActionPart{ID: "cordon", Title: node.GetName(), Outcome: core.OutcomeRefused, Why: &why})
	}
	for attempt := 1; ; attempt++ {
		n, i, err := s.drainCheck(ctx, def, run)
		if err != nil && !sent {
			return core.ActionResult{}, err
		}
		if err != nil {
			stopped(cordonStopWhy(ctx, err))
			break
		}
		node, inv = n, i
		if boolAt(node.Object, "spec", "unschedulable") {
			break
		}
		if err := ctx.Err(); err != nil && !sent {
			return core.ActionResult{}, provider.Said(provider.ClassUnavailable, msg("error.nothingWritten", "detail", err.Error()))
		} else if err != nil {
			stopped(stopWhy(ctx))
			break
		}
		part, retry, err := s.drainCordon(ctx, def, run, node, attempt)
		sent = true
		if err != nil {
			stopped(cordonStopWhy(ctx, err))
			break
		}
		if retry {
			continue
		}
		parts = append(parts, part)
		break
	}
	if len(parts) > 0 && parts[0].Outcome != core.OutcomeDone {
		for _, p := range inv.evict {
			parts = append(parts, skippedPart(p, msg("drain.why.notCordoned")))
		}
	} else {
		for _, p := range inv.evict {
			parts = append(parts, s.evictOne(ctx, p)) // not started once the run is over
		}
	}
	for _, p := range inv.bare {
		parts = append(parts, skippedPart(p, msg("drain.why.bare")))
	}
	out := core.PartsOutcome(parts)
	done := 0
	for _, p := range parts {
		if p.Outcome == core.OutcomeDone {
			done++
		}
	}
	return core.ActionResult{Message: msg("done.drain", "name", node.GetName(), "done", done, "total", len(parts)), Outcome: out, Parts: parts}, nil
}

// drainCheck reads the node (by UID) and its pods again: they must be the
// plan's.
func (s *session) drainCheck(ctx context.Context, def *kindDef, run provider.ActionRun) (*unstructured.Unstructured, *drainInventory, error) {
	node, err := s.getConfirmed(ctx, def, run.Ref)
	if err != nil {
		return nil, nil, err
	}
	if why := actionUnavailable(def, actDrain.ID, node); why != nil {
		return nil, nil, provider.Said(provider.ClassConflict, *why)
	}
	inv, why := s.drainPods(ctx, node.GetName())
	if why != nil {
		return nil, nil, provider.Said(provider.ClassConflict, notWritten(*why))
	}
	if drainExpect(def, node, inv) != run.Expect {
		return nil, nil, provider.Said(provider.ClassConflict, msg("drain.changed", "name", node.GetName()))
	}
	return node, inv, nil
}

// cordonStopWhy: why a sent cordon ended without being written — its node
// gone, the plan no longer the reviewed one, the run's time up.
func cordonStopWhy(ctx context.Context, err error) core.Message {
	var pe *provider.Error
	switch {
	case ctx.Err() != nil:
		return stopWhy(ctx)
	case errors.As(err, &pe) && pe.Class == provider.ClassGone:
		return msg("drain.why.nodeGone")
	case errors.As(err, &pe) && pe.Class == provider.ClassConflict:
		return msg("drain.why.planChanged")
	case errors.As(err, &pe):
		return msg("drain.why.refused", "detail", pe.Message)
	}
	return msg("drain.why.refused", "detail", shortErr(err))
}

// drainCordon writes the cordon; retry: a version-only change refused it
// (the whole plan checked again before). An error: the node is gone.
func (s *session) drainCordon(ctx context.Context, def *kindDef, run provider.ActionRun, node *unstructured.Unstructured, attempt int) (core.ActionPart, bool, error) {
	part := core.ActionPart{ID: "cordon", Title: node.GetName()}
	wctx, cancel := context.WithTimeout(ctx, drainWriteTimeout)
	defer cancel()
	cordon := provider.ActionRun{Ref: run.Ref, Action: actCordon.ID}
	_, err := s.write(wctx, def, cordon, node)
	if err == nil {
		part.Outcome = core.OutcomeDone
		return part, false, nil
	}
	var se apierrors.APIStatus
	switch {
	case !errors.As(err, &se) || ambiguous(err):
		part.Outcome, part.Why = core.OutcomeUnknown, ptr(msg("drain.why.unknown", "detail", shortErr(err)))
		return part, false, nil
	case apierrors.IsNotFound(err):
		return part, false, provider.Said(provider.ClassGone, msg("error.gone", "kind", "node", "name", node.GetName()))
	case apierrors.IsConflict(err) && attempt < maxVersionRetries:
		// Checked again from the start (node and pods); a moved version
		// with the same plan is tried again.
		now, gerr := s.getConfirmed(ctx, def, run.Ref)
		if gerr == nil && now.GetResourceVersion() != node.GetResourceVersion() {
			return part, true, nil
		}
	}
	part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.refused", "detail", statusMessage(err)))
	return part, false, nil
}

// evictOne asks for p's eviction with the UID and version of this run's
// list. A 404 or 409 proves nothing alone: one read of the pod tells gone
// or replaced (skipped) from refused; only a 409 at a moved version with
// the same fingerprint is tried again (at most maxVersionRetries in all).
func (s *session) evictOne(ctx context.Context, p drainPod) core.ActionPart {
	part := core.ActionPart{ID: string(p.u.GetUID()), Title: p.name()}
	ns, name, uid, rv := p.u.GetNamespace(), p.u.GetName(), string(p.u.GetUID()), p.u.GetResourceVersion()
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	for attempt := 1; ; attempt++ {
		if ctx.Err() != nil {
			return skippedPart(p, stopWhy(ctx))
		}
		wctx, cancel := context.WithTimeout(ctx, drainWriteTimeout)
		err := wr.evict(wctx, ns, name, uid, rv)
		cancel()
		var se apierrors.APIStatus
		switch {
		case err == nil:
			part.Outcome = core.OutcomeDone
			return part
		case !errors.As(err, &se) || ambiguous(err):
			part.Outcome, part.Why = core.OutcomeUnknown, ptr(msg("drain.why.unknown", "detail", shortErr(err)))
			return part
		case apierrors.IsTooManyRequests(err):
			part.Outcome = core.OutcomeRefused
			part.Why = ptr(msg("drain.why.throttled"))
			if d := se.Status().Details; d != nil {
				for _, c := range d.Causes {
					if string(c.Type) == disruptionBudgetCause {
						part.Why = ptr(msg("drain.why.pdb"))
					}
				}
			}
			return part
		case !apierrors.IsNotFound(err) && !apierrors.IsConflict(err):
			part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.refused", "detail", statusMessage(err)))
			return part
		}
		// 404 or 409: look at the pod once.
		gctx, gcancel := context.WithTimeout(ctx, drainWriteTimeout)
		now, gerr := s.dyn.Resource(podsKind.gvr).Namespace(ns).Get(gctx, name, metav1.GetOptions{})
		gcancel()
		switch {
		case gerr != nil && apierrors.IsNotFound(gerr):
			return skippedPart(p, msg("drain.why.gone"))
		case gerr != nil:
			part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.refused", "detail", statusMessage(err)))
			return part
		case string(now.GetUID()) != uid:
			return skippedPart(p, msg("drain.why.replaced"))
		case apierrors.IsNotFound(err), now.GetResourceVersion() == rv:
			// A live pod and a 404 (no eviction route?), or a 409 at the
			// version asked about: refused, not proof of anything else.
			part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.refused", "detail", statusMessage(err)))
			return part
		}
		q := classifyPod(now)
		if b1, _ := json.Marshal(q.print()); string(b1) != string(printJSON(p.print())) {
			part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.changed"))
			return part
		}
		if attempt >= maxVersionRetries {
			part.Outcome, part.Why = core.OutcomeRefused, ptr(msg("drain.why.keepsChanging"))
			return part
		}
		rv = now.GetResourceVersion()
	}
}

func printJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func skippedPart(p drainPod, why core.Message) core.ActionPart {
	return core.ActionPart{ID: string(p.u.GetUID()), Title: p.name(), Outcome: core.OutcomeSkipped, Why: &why}
}

// stopWhy: why parts were not started — the run's time ran out or its
// caller went away.
func stopWhy(ctx context.Context) core.Message {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return msg("drain.why.timeUp")
	}
	return msg("drain.why.cancelled")
}

func ptr[T any](v T) *T { return &v }

// notWritten is why (a reason the node's pods cannot be drained) said at
// a run: nothing was written. A reason without its run form is said as
// text: a run never panics over a missing sentence.
func notWritten(why core.Message) core.Message {
	key := strings.TrimPrefix(why.Key, ProviderID+".") + "NotWritten"
	if _, ok := messageTexts[key]; !ok {
		return core.Message{Text: why.Text + "; nothing was written"}
	}
	kv := make([]any, 0, 2*len(why.Params))
	for k, v := range why.Params {
		kv = append(kv, k, v)
	}
	return msg(key, kv...)
}
