package kubernetes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// Deployment rollout actions (P15): undo to a chosen revision (kubectl
// rollout undo --to-revision), pause and resume (kubectl rollout
// pause/resume; resume shares its ID with the CronJob's). A revision is a
// ReplicaSet the Deployment controls, named by the ReplicaSet (revision
// numbers move with every rollback).
var (
	actUndo  = core.ActionDescriptor{ID: "undo", Title: "Roll back", Param: &core.ActionParam{Kind: core.ParamChoice}}
	actPause = core.ActionDescriptor{ID: "pause", Title: "Pause rollout"}
)

const (
	revisionKey    = "deployment.kubernetes.io/revision"
	changeCauseKey = "kubernetes.io/change-cause"
	templateHash   = "pod-template-hash"
	// maxChoices: the newest revisions offered (revisionHistoryLimit is 10
	// by default).
	maxChoices = 50
)

// revision is a ReplicaSet of a Deployment with its revision number.
type revision struct {
	rs *unstructured.Unstructured
	n  int64
	// template: the ReplicaSet's pod template without its pod-template-hash.
	template map[string]any
}

// revisions lists the ReplicaSets u controls that carry a revision, the
// newest first (scaled to zero too: they are the history); trunc: more
// may exist than were read.
func (s *session) revisions(ctx context.Context, u *unstructured.Unstructured) ([]revision, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	var trunc bool
	rss, err := s.owned(ctx, replicaSetsKind, u, &trunc)
	if err != nil {
		return nil, false, err
	}
	out := make([]revision, 0, len(rss))
	for i := range rss {
		n, err := strconv.ParseInt(rss[i].GetAnnotations()[revisionKey], 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		out = append(out, revision{rs: &rss[i], n: n, template: withoutHash(fieldAt(rss[i].Object, "spec", "template"))})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].n > out[j].n })
	return out, trunc, nil
}

// withoutHash: a copy of a pod template without the pod-template-hash
// label the Deployment controller adds to its ReplicaSets' templates (not
// the selector's: that is not part of the template).
func withoutHash(t any) map[string]any {
	m, ok := t.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	c := runtimeDeepCopy(m)
	if l, ok := fieldAt(c, "metadata", "labels").(map[string]any); ok {
		delete(l, templateHash)
		if len(l) == 0 {
			delete(c["metadata"].(map[string]any), "labels")
		}
	}
	return c
}

// sameTemplate: kubectl's EqualIgnoreHash (both read from the API server:
// defaulted alike).
func sameTemplate(a, b map[string]any) bool { return reflect.DeepEqual(a, b) }

func templateSum(t map[string]any) string {
	b, _ := json.Marshal(t)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// undoState: the chosen revision and the Expect of an undo to it — the
// route, the action and choice, what the effects read of the Deployment
// (identity, deletion, paused, template, replicas, strategy) and of the
// revision (identity, number, template), the newest revision (the number
// it becomes). Versions are not part of it: status churn is retried.
func undoState(def *kindDef, choice string, u *unstructured.Unstructured, revs []revision) (*revision, string) {
	var chosen *revision
	var newest int64
	for i := range revs {
		newest = max(newest, revs[i].n)
		if revs[i].rs.GetName() == choice {
			chosen = &revs[i]
		}
	}
	o := u.Object
	st := map[string]any{
		"uid": string(u.GetUID()), "deleting": u.GetDeletionTimestamp() != nil, "paused": boolAt(o, "spec", "paused"),
		"template": templateSum(withoutHash(fieldAt(o, "spec", "template"))), "replicas": replicas(o), "strategy": fieldAt(o, "spec", "strategy"),
		"newest": newest,
	}
	if chosen != nil {
		st["rs"] = map[string]any{"uid": string(chosen.rs.GetUID()), "n": chosen.n, "template": templateSum(chosen.template)}
	}
	b, _ := json.Marshal(map[string]any{"action": actUndo.ID, "choice": choice, "state": st})
	sum := sha256.Sum256(b)
	return chosen, routeOf(def) + "-" + hex.EncodeToString(sum[:12])
}

// prepareUndo offers the revisions; with one chosen, says what the undo
// does and what changes in the template.
func (s *session) prepareUndo(ctx context.Context, def *kindDef, plan core.ActionPlan, u *unstructured.Unstructured) core.ActionPlan {
	revs, trunc, err := s.revisions(ctx, u)
	if err != nil {
		m := msg("undo.noRevisions", "error", err.Error())
		plan.Unavailable = &m
		return plan
	}
	cur := withoutHash(fieldAt(u.Object, "spec", "template"))
	for i, r := range revs {
		if i == maxChoices {
			plan.Warnings = append(plan.Warnings, countMsg("undo.olderOne", "undo.older", len(revs)-maxChoices))
			break
		}
		plan.Choices = append(plan.Choices, revisionChoice(r, sameTemplate(r.template, cur)))
	}
	if trunc {
		plan.Warnings = append(plan.Warnings, msg("undo.notAll"))
	}
	if len(revs) == 0 {
		m := msg("undo.none", "name", u.GetName())
		plan.Unavailable = &m
		return plan
	}
	if plan.Params.Choice == nil {
		plan.Effects = []core.Message{msg("undo.choose")}
		return plan
	}
	chosen, expect := undoState(def, *plan.Params.Choice, u, revs)
	plan.Expect = expect
	switch {
	case plan.Unavailable != nil:
		return plan
	case chosen == nil:
		m := msg("undo.gone", "name", *plan.Params.Choice)
		plan.Unavailable = &m
		return plan
	case sameTemplate(chosen.template, cur):
		m := msg("undo.isCurrent", "revision", chosen.n)
		plan.Unavailable = &m
		return plan
	}
	plan.Effects = []core.Message{msg("undo.effect", "revision", chosen.n, "next", revs[0].n+1), restartEffect(def, u.Object)}
	plan.Changes = templateChanges(cur, chosen.template)
	return plan
}

func revisionChoice(r revision, current bool) core.ActionChoice {
	o := r.rs.Object
	c := core.ActionChoice{Value: r.rs.GetName(), Title: msg("undo.revision", "revision", r.n), Current: current}
	if t := r.rs.GetCreationTimestamp(); !t.IsZero() {
		c.At = t.UnixMilli()
	}
	if cause := r.rs.GetAnnotations()[changeCauseKey]; cause != "" {
		c.Details = append(c.Details, msg("undo.cause", "cause", cause))
	}
	var images []string
	for _, ct := range slice(r.template, "spec", "containers") {
		images = append(images, str(ct, "image"))
	}
	if len(images) > 0 {
		c.Details = append(c.Details, msg("undo.images", "images", strings.Join(images, ", ")))
	}
	if n := i64(o, "status", "replicas"); n > 0 { // an old revision runs none: not said
		c.Details = append(c.Details, msg("undo.pods", "ready", i64(o, "status", "readyReplicas"), "total", n))
	}
	if current {
		m := msg("undo.isCurrent", "revision", r.n)
		c.Unavailable = &m
	}
	return c
}

// runUndo: each attempt reads the Deployment by UID and its revisions
// again, checks the plan's Expect against both and writes the template of
// the revision read in that attempt.
func (s *session) runUndo(ctx context.Context, def *kindDef, run provider.ActionRun) (core.ActionResult, error) {
	if !sameRoute(def, run.Expect) {
		return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.routeChanged", "kind", def.desc.Title))
	}
	choice := *run.Params.Choice // CheckParams: a run carries one
	expect := func(ctx context.Context, u *unstructured.Unstructured) (*revision, string, error) {
		revs, _, err := s.revisions(ctx, u)
		if err != nil {
			return nil, "", &provider.Error{Class: provider.ClassUnavailable, Message: err.Error()}
		}
		r, e := undoState(def, choice, u, revs)
		return r, e, nil
	}
	for attempt := 0; ; attempt++ {
		u, err := s.getConfirmed(ctx, def, run.Ref)
		if err != nil {
			return core.ActionResult{}, err
		}
		if why := actionUnavailable(def, run.Action, u); why != nil {
			return core.ActionResult{}, provider.Said(provider.ClassConflict, *why)
		}
		chosen, e, err := expect(ctx, u)
		switch {
		case err != nil:
			return core.ActionResult{}, err
		case chosen == nil:
			return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("undo.gone", "name", choice))
		case e != run.Expect:
			return core.ActionResult{}, provider.Said(provider.ClassConflict, msg("error.changed", "kind", singular(def), "name", u.GetName()))
		}
		if s.beforeWrite != nil {
			s.beforeWrite(run.Action, u)
		}
		err = s.writeUndo(ctx, def, u, chosen)
		if err == nil {
			return core.ActionResult{Message: msg("done.undo", "kind", singular(def), "name", u.GetName(), "revision", chosen.n)}, nil
		}
		retry, err := s.failedWrite(ctx, def, run, u, err, func(ctx context.Context, now *unstructured.Unstructured) (string, error) {
			_, e, err := expect(ctx, now)
			return e, err
		})
		if !retry || attempt == maxVersionRetries {
			return core.ActionResult{}, err
		}
	}
}

// writeUndo: one JSON Patch — the UID tested; the version as the update's
// precondition (replace: a stale one is a 409, retried; a failed test op
// would be a bare 422, never retried); the revision's template; the
// change-cause as the revision's (no other annotation is touched).
func (s *session) writeUndo(ctx context.Context, def *kindDef, u *unstructured.Unstructured, r *revision) error {
	ctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	ops := []map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": string(u.GetUID())},
		{"op": "replace", "path": "/metadata/resourceVersion", "value": u.GetResourceVersion()},
		{"op": "replace", "path": "/spec/template", "value": r.template},
	}
	cause, has := r.rs.GetAnnotations()[changeCauseKey]
	ann := u.GetAnnotations()
	_, had := ann[changeCauseKey]
	causePath := "/metadata/annotations/" + strings.ReplaceAll(strings.ReplaceAll(changeCauseKey, "~", "~0"), "/", "~1")
	switch {
	case has && ann == nil:
		ops = append(ops, map[string]any{"op": "add", "path": "/metadata/annotations", "value": map[string]any{changeCauseKey: cause}})
	case has:
		ops = append(ops, map[string]any{"op": "add", "path": causePath, "value": cause})
	case had:
		ops = append(ops, map[string]any{"op": "remove", "path": causePath})
	}
	body, err := json.Marshal(ops)
	if err != nil {
		return err
	}
	wr := s.writer
	if wr == nil {
		wr = dynWriter{s.dyn}
	}
	return wr.patch(ctx, def.gvr, u.GetNamespace(), u.GetName(), types.JSONPatchType, body, "")
}

// pauseUnavailable: pause of a paused Deployment, resume of one that is not.
func pauseUnavailable(action string, u *unstructured.Unstructured) *core.Message {
	paused := boolAt(u.Object, "spec", "paused")
	switch {
	case action == actPause.ID && paused:
		m := msg("unavailable.alreadyPaused", "name", u.GetName())
		return &m
	case action == actResume.ID && !paused:
		m := msg("unavailable.notPaused", "name", u.GetName())
		return &m
	case action == actUndo.ID && paused:
		// kubectl refuses too: the rollback would not roll out.
		m := msg("unavailable.paused", "name", u.GetName())
		return &m
	}
	return nil
}

// templateChanges: the difference of two pod templates as flat lines —
// what is set, added, removed; lists of named items (containers, env,
// volumes) by name; an env item as its value or its reference (the
// templates are all that is read: a Secret's value never is).
func templateChanges(from, to map[string]any) []core.Message {
	var out []core.Message
	diffValue(&out, fieldAt(from, "metadata"), fieldAt(to, "metadata"), "metadata")
	diffValue(&out, fieldAt(from, "spec"), fieldAt(to, "spec"), "")
	return out
}

var plainKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

func joinPath(path, key string) string {
	if !plainKey.MatchString(key) {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

func diffValue(out *[]core.Message, a, b any, path string) {
	switch {
	case reflect.DeepEqual(a, b):
		return
	case a == nil:
		if l, ok := b.([]any); ok {
			if bn, ok := byName(l); ok && len(l) > 0 {
				diffNamed(out, path, nil, l, nil, bn) // item by item
				return
			}
		}
		if m, ok := b.(map[string]any); ok && len(m) > 0 {
			diffValue(out, map[string]any{}, m, path) // what was added, key by key
			return
		}
		*out = append(*out, addedMsg(path, b))
		return
	case b == nil:
		if l, ok := a.([]any); ok {
			if an, ok := byName(l); ok && len(l) > 0 {
				diffNamed(out, path, l, nil, an, map[string]map[string]any{})
				return
			}
		}
		*out = append(*out, msg("change.removed", "path", path))
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := make([]string, 0, len(am)+len(bm))
		for k := range am {
			keys = append(keys, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			diffValue(out, am[k], bm[k], joinPath(path, k))
		}
		return
	}
	al, aok := a.([]any)
	bl, bok := b.([]any)
	if aok && bok {
		if an, ok := byName(al); ok {
			if bn, ok := byName(bl); ok {
				diffNamed(out, path, al, bl, an, bn)
				return
			}
		}
		*out = append(*out, msg("change.changed", "path", path))
		return
	}
	if isScalar(a) && isScalar(b) {
		*out = append(*out, msg("change.set", "path", path, "from", short(scalarText(a)), "to", short(scalarText(b))))
		return
	}
	*out = append(*out, msg("change.changed", "path", path))
}

// diffNamed: items of b in their order, then those only in a.
func diffNamed(out *[]core.Message, path string, al, bl []any, an, bn map[string]map[string]any) {
	env := strings.HasSuffix(path, "env")
	item := func(name string) string { return path + "[" + name + "]" }
	for _, x := range bl {
		m := x.(map[string]any)
		name := str(m, "name")
		old, was := an[name]
		switch {
		case env && !was:
			*out = append(*out, msg("change.added", "path", item(name), "value", short(envValue(m))))
		case env && !reflect.DeepEqual(old, m):
			*out = append(*out, msg("change.set", "path", item(name), "from", short(envValue(old)), "to", short(envValue(m))))
		case !was:
			*out = append(*out, msg("change.addedPlain", "path", item(name)))
		default:
			diffValue(out, withoutName(old), withoutName(m), item(name))
		}
	}
	for _, x := range al {
		name := str(x.(map[string]any), "name")
		if _, ok := bn[name]; !ok {
			*out = append(*out, msg("change.removed", "path", item(name)))
		}
	}
}

func withoutName(m map[string]any) map[string]any {
	c := make(map[string]any, len(m))
	for k, v := range m {
		if k != "name" {
			c[k] = v
		}
	}
	return c
}

// byName: a list of maps each with a distinct name.
func byName(l []any) (map[string]map[string]any, bool) {
	out := make(map[string]map[string]any, len(l))
	for _, x := range l {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, false
		}
		name, ok := m["name"].(string)
		if !ok || name == "" {
			return nil, false
		}
		if _, dup := out[name]; dup {
			return nil, false
		}
		out[name] = m
	}
	return out, true
}

// envValue: an env item's value, or what it refers to.
func envValue(m map[string]any) string {
	if v, ok := m["value"]; ok {
		return scalarText(v)
	}
	ref := func(kind string) (string, bool) {
		r, ok := fieldAt(m, "valueFrom", kind).(map[string]any)
		if !ok {
			return "", false
		}
		return kind + " " + str(r, "name") + "/" + str(r, "key"), true
	}
	for _, k := range []string{"secretKeyRef", "configMapKeyRef"} {
		if s, ok := ref(k); ok {
			return s
		}
	}
	if p := str(m, "valueFrom", "fieldRef", "fieldPath"); p != "" {
		return "fieldRef " + p
	}
	if r := str(m, "valueFrom", "resourceFieldRef", "resource"); r != "" {
		return "resourceFieldRef " + r
	}
	return ""
}

func addedMsg(path string, v any) core.Message {
	if isScalar(v) {
		return msg("change.added", "path", path, "value", short(scalarText(v)))
	}
	return msg("change.addedPlain", "path", path)
}

func isScalar(v any) bool {
	switch v.(type) {
	case string, bool, int64, float64, int:
		return true
	}
	return false
}

func scalarText(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// short: a value within a line.
func short(s string) string {
	const n = 200
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
