package kubernetes

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// editFormat is the version of editView: a base made by another one does
// not match the text it would show now.
const editFormat = 1

// prepareEditTimeout bounds a review: reads, proofs and the dry run (a
// variable for tests).
var prepareEditTimeout = 30 * time.Second

// maxEditText bounds an edited document (an object in etcd is ≤ 1.5 MB).
const maxEditText = 3 << 20

var _ provider.Editor = (*session)(nil)

// editable: objects of def can be edited as text — API resources that
// serve patch; not events, not views made of other kinds.
func editable(def *kindDef) bool {
	if def == nil || def.virtual || def == eventsKind {
		return false
	}
	if !def.discovered {
		return true
	}
	for _, v := range def.verbs {
		if v == "patch" {
			return true
		}
	}
	return false
}

// editRoute is where an edit reads and writes (the resource's version and
// scope: a discovered kind may move).
func editRoute(def *kindDef) string { return fmt.Sprintf("%s|%t", def.gvr, def.namespaced) }

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// editGet reads the object to edit. A Secret's refusal is said without the
// server's strings (secretSafe): a message may carry a value.
func (s *session) editGet(ctx context.Context, def *kindDef, ref core.Ref) (*unstructured.Unstructured, error) {
	u, err := s.readObject(ctx, def, ref)
	if err == nil {
		return u, nil
	}
	var se apierrors.APIStatus
	if def == secretsKind && errors.As(err, &se) {
		class, _ := classify(err)
		return nil, &provider.Error{Class: class, Message: secretReadSafe(err).Text}
	}
	var pe *provider.Error
	if errors.As(err, &pe) {
		return nil, err
	}
	class, text := classify(err)
	return nil, &provider.Error{Class: class, Message: text}
}

func (s *session) editTarget(ref core.Ref) (*kindDef, error) {
	def := s.kind(ref.Kind)
	if !editable(def) {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s cannot be edited here", ref.Kind)}
	}
	return def, nil
}

// EditSource reads the object's text for the editor.
func (s *session) EditSource(ctx context.Context, ref core.Ref) (core.EditDoc, provider.EditBase, error) {
	def, err := s.editTarget(ref)
	if err != nil {
		return core.EditDoc{}, provider.EditBase{}, err
	}
	u, err := s.editGet(ctx, def, ref)
	if err != nil {
		return core.EditDoc{}, provider.EditBase{}, err
	}
	text, err := editView(u, def == secretsKind)
	if err != nil {
		return core.EditDoc{}, provider.EditBase{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	ref.UID, ref.Scope, ref.Name = string(u.GetUID()), u.GetNamespace(), u.GetName()
	base := provider.EditBase{
		Route: editRoute(def), APIVersion: u.GetAPIVersion(), Kind: u.GetKind(),
		Namespace: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID()), Version: u.GetResourceVersion(),
		DocHash: sha([]byte(text)), Format: editFormat,
	}
	return core.EditDoc{Ref: ref, Text: text, Version: u.GetResourceVersion()}, base, nil
}

// editCheck is a request checked against its base: the kind, the original
// as shown, the patch and its canonical bytes.
type editCheck struct {
	def   *kindDef
	ref   core.Ref
	orig  map[string]any
	patch map[string]any
	// canon is the patch's canonical JSON (sorted keys, exact numbers).
	canon []byte
}

func badEdit(format string, a ...any) error {
	return &provider.Error{Class: provider.ClassInvalid, Message: fmt.Sprintf(format, a...)}
}

func (s *session) checkEdit(req provider.EditRequest) (*editCheck, error) {
	def, err := s.editTarget(req.Ref)
	if err != nil {
		return nil, err
	}
	b := req.Base
	if b.Route != editRoute(def) || b.Format != editFormat {
		return nil, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("the API resource of %s changed since the editor opened: open it again", def.desc.Title)}
	}
	if len(req.Original) > maxEditText || len(req.Edited) > maxEditText {
		return nil, badEdit("the text is over %d MB", maxEditText>>20)
	}
	if sha([]byte(req.Original)) != b.DocHash {
		return nil, badEdit("the original text is not the one the editor was given")
	}
	if req.Ref.Name != b.Name || (def.namespaced && req.Ref.Scope != b.Namespace) || (req.Ref.UID != "" && req.Ref.UID != b.UID) {
		return nil, badEdit("the object is not the one the editor was given")
	}
	orig, err := parseEditDoc(req.Original)
	if err != nil {
		return nil, badEdit("the original text: %v", err)
	}
	edited, err := parseEditDoc(req.Edited)
	if err != nil {
		return nil, badEdit("%v", err)
	}
	id := editIdentity{APIVersion: b.APIVersion, Kind: b.Kind, Name: b.Name, Namespace: b.Namespace, UID: b.UID, Secret: def == secretsKind}
	patch, err := buildEditPatch(id, orig, edited)
	if err != nil {
		return nil, badEdit("%v", err)
	}
	canon, err := json.Marshal(patch)
	if err != nil {
		return nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	ref := req.Ref
	ref.UID, ref.Scope, ref.Name = b.UID, b.Namespace, b.Name
	return &editCheck{def: def, ref: ref, orig: orig, patch: patch, canon: canon}, nil
}

// withPreconditions: the patch pinned to the object's UID and version.
func withPreconditions(patch map[string]any, uid, version string) ([]byte, error) {
	out := make(map[string]any, len(patch)+1)
	for k, v := range patch {
		out[k] = v
	}
	meta := map[string]any{}
	if m, ok := patch["metadata"].(map[string]any); ok {
		for k, v := range m {
			meta[k] = v
		}
	}
	meta["uid"], meta["resourceVersion"] = uid, version
	out["metadata"] = meta
	return json.Marshal(out)
}

// asTree is o as JSON values with exact numbers (json.Number).
func asTree(o map[string]any) (map[string]any, error) {
	b, err := json.Marshal(o)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	return out, dec.Decode(&out)
}

// PrepareEdit reads what the edit would do; nothing changes. A server dry
// run is asked only where it is proven (dryRunProofs); elsewhere the patch
// is laid over the object locally.
func (s *session) PrepareEdit(ctx context.Context, req provider.EditRequest) (core.EditPlan, *provider.EditGrant, error) {
	c, err := s.checkEdit(req)
	if err != nil {
		return core.EditPlan{}, nil, err
	}
	// A review ends in time and with the session: its reads, proofs and dry
	// run are given up, and no grant outlives the connection it was made on.
	ctx, cancel := context.WithTimeout(ctx, prepareEditTimeout)
	defer cancel()
	defer context.AfterFunc(s.ctx, cancel)()
	plan, grant, err := s.prepareEdit(ctx, c, req)
	switch {
	case s.ctx.Err() != nil:
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the connection was closed during the review: review it again"}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the review took too long: try again"}
	case ctx.Err() != nil:
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the review was cancelled"}
	}
	return plan, grant, err
}

func (s *session) prepareEdit(ctx context.Context, c *editCheck, req provider.EditRequest) (core.EditPlan, *provider.EditGrant, error) {
	def, secret := c.def, c.def == secretsKind
	cur, err := s.editGet(ctx, def, c.ref)
	if err != nil {
		return core.EditPlan{}, nil, err
	}
	before, err := editView(cur, secret)
	if err != nil {
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	plan := core.EditPlan{
		Where: core.LiveTarget{Provider: ProviderID, Target: s.conn.target, TargetTitle: s.conn.targetTitle, Endpoint: s.conn.endpoint,
			ConfigHash: s.hash, Ref: c.ref},
		Before: before, After: before,
	}
	if len(c.patch) == 0 {
		return plan, nil, nil
	}
	plan.Changed = true
	plan.Rebased = cur.GetResourceVersion() != req.Base.Version
	curDoc, err := parseEditDoc(before)
	if err != nil {
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	plan.Collisions = collisions(c.patch, withoutVersion(c.orig), withoutVersion(curDoc))

	// The patch over the whole current object: it must leave what the
	// editor hides as it is (checked before anything is sent).
	full, err := asTree(cur.Object)
	if err != nil {
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	local := applyMergePatch(full, c.patch)
	if !sameHidden(full, local) {
		return core.EditPlan{}, nil, badEdit("the edit would change fields the editor does not show")
	}
	send, err := withPreconditions(c.patch, string(cur.GetUID()), cur.GetResourceVersion())
	if err != nil {
		return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	mode := "local"
	result := &unstructured.Unstructured{Object: local}
	if s.edits.proven(ctx, s.cat.get, def) {
		body, werr := s.editWriter().editPatch(ctx, def.gvr, cur.GetNamespace(), cur.GetName(), send, true)
		switch {
		case werr == nil:
			u := &unstructured.Unstructured{}
			if err := u.UnmarshalJSON(body); err != nil {
				return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: fmt.Sprintf("the dry run's answer: %v", err)}
			}
			result, mode, plan.Checked = u, "checked", true
		case noDryRun(werr):
			plan.Warnings = append(plan.Warnings, msg("edit.noDryRunWebhook"))
		case apierrors.IsConflict(werr):
			return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("%s %s changed while the edit was being checked: review it again", singular(def), cur.GetName())}
		case apierrors.IsNotFound(werr):
			return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s %s no longer exists", singular(def), cur.GetName())}
		case apierrors.IsInvalid(werr) || apierrors.IsBadRequest(werr) || apierrors.IsForbidden(werr):
			m := refusal(werr, secret)
			plan.Unavailable = &m
		default:
			class, text := classify(werr)
			if secret {
				text = secretSafe(werr).Text
			}
			return core.EditPlan{}, nil, &provider.Error{Class: class, Message: text}
		}
	} else {
		plan.Warnings = append(plan.Warnings, msg("edit.local"))
	}
	if plan.Unavailable == nil {
		if plan.After, err = editView(result, secret); err != nil {
			return core.EditPlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
		}
	}
	if !plan.Checked {
		plan.Destructive = true
	}
	if plan.Rebased {
		plan.Warnings = append(plan.Warnings, msg("edit.rebased"))
	}
	if len(plan.Collisions) > 0 {
		plan.Destructive = true
		plan.Warnings = append(plan.Warnings, msg("edit.collisions", "paths", strings.Join(plan.Collisions, ", ")))
	}
	fx := editEffects(def, cur, c.patch, local)
	plan.Warnings = append(plan.Warnings, fx.warnings...)
	plan.Destructive = plan.Destructive || fx.destructive

	ectx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	rights := make(chan core.Rights, 1)
	go func() { rights <- s.rights(ectx, def, "edit", cur) }()
	select {
	case plan.Rights = <-rights:
	case <-ectx.Done():
		plan.Rights = core.Rights{State: core.RightsUnknown, Reason: "the check took too long"}
	}
	if plan.Unavailable != nil || plan.Rights.State == core.RightsDenied {
		return plan, nil, nil
	}
	return plan, &provider.EditGrant{
		Route: editRoute(def), Namespace: cur.GetNamespace(), Name: cur.GetName(), UID: string(cur.GetUID()),
		Version: cur.GetResourceVersion(), PatchHash: sha(c.canon), Mode: mode,
	}, nil
}

// RunEdit writes a reviewed edit once: the same patch, pinned to the UID
// and the version the review was made against (never a newer one).
func (s *session) RunEdit(ctx context.Context, run provider.EditRun) (core.EditResult, error) {
	c, err := s.checkEdit(run.EditRequest)
	if err != nil {
		return core.EditResult{}, err
	}
	g, def, secret := run.Grant, c.def, c.def == secretsKind
	if g.Route != editRoute(def) {
		return core.EditResult{}, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("the API resource of %s changed since the edit was reviewed: review it again", def.desc.Title)}
	}
	if g.UID != run.Base.UID || g.Name != run.Base.Name || g.Namespace != run.Base.Namespace || g.Version == "" {
		return core.EditResult{}, badEdit("the review is of another object")
	}
	if g.PatchHash != sha(c.canon) {
		return core.EditResult{}, badEdit("the edit is not the one reviewed: review it again")
	}
	send, err := withPreconditions(c.patch, g.UID, g.Version)
	if err != nil {
		return core.EditResult{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	name := fmt.Sprintf("%s %s", singular(def), g.Name)
	wctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	body, werr := s.editWriter().editPatch(wctx, def.gvr, g.Namespace, g.Name, send, false)
	if werr != nil {
		return core.EditResult{}, editWriteError(werr, name, secret)
	}
	out := core.EditResult{Message: name + ": changes written"}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(body); err == nil {
		out.Version = u.GetResourceVersion()
		if a, err := editView(u, secret); err == nil {
			out.Actual = a
		}
	}
	return out, nil
}

// editWriteError classifies a failed write: never retried; an answer that
// does not say whether it was applied is unknown.
func editWriteError(err error, name string, secret bool) error {
	text := func() string {
		if secret {
			return secretSafe(err).Text
		}
		return statusMessage(err)
	}
	var se apierrors.APIStatus
	switch {
	case !errors.As(err, &se) && strings.Contains(err.Error(), "getting credentials"): // before sending
		class, msg := classify(err)
		return &provider.Error{Class: class, Message: msg}
	case !errors.As(err, &se):
		return &provider.Error{Class: provider.ClassUnknown, Message: fmt.Sprintf("the result is not known: check %s before editing again", name)}
	case apierrors.IsConflict(err):
		return &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("%s changed since the edit was reviewed: review it again", name)}
	case apierrors.IsNotFound(err):
		return &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("%s no longer exists", name)}
	case apierrors.IsForbidden(err):
		return &provider.Error{Class: provider.ClassForbidden, Message: text()}
	case apierrors.IsInvalid(err) || apierrors.IsBadRequest(err):
		return &provider.Error{Class: provider.ClassInvalid, Message: text()}
	case ambiguous(err):
		return &provider.Error{Class: provider.ClassUnknown, Message: fmt.Sprintf("the result is not known: check %s before editing again", name)}
	}
	class, _ := classify(err)
	return &provider.Error{Class: class, Message: text()}
}

// refusal is why the server refused the dry run (a Secret's: in the
// provider's words only).
func refusal(err error, secret bool) core.Message {
	if secret {
		return secretSafe(err)
	}
	return core.Message{Text: statusMessage(err)}
}

// noDryRun: an admission webhook with side effects cannot take a dry run
// (the API server's own refusal; nothing else falls back to local).
func noDryRun(err error) bool {
	return apierrors.IsBadRequest(err) && dryRunUnsupported.MatchString(statusMessage(err))
}

// dryRunUnsupported is the API server's refusal, exactly
// (webhookerrors.NewDryRunUnsupportedErr); a webhook's own denial may say
// the same words.
var dryRunUnsupported = regexp.MustCompile(`^admission webhook "(?:[^"\\]|\\.)*" does not support dry run$`)

func (s *session) editWriter() actionWriter {
	if s.writer != nil {
		return s.writer
	}
	return dynWriter{s.dyn}
}

// applyMergePatch lays patch over doc (RFC 7386); doc is not changed.
func applyMergePatch(doc, patch map[string]any) map[string]any {
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = v
	}
	for k, p := range patch {
		switch pv := p.(type) {
		case nil:
			delete(out, k)
		case map[string]any:
			d, _ := out[k].(map[string]any)
			if d == nil {
				d = map[string]any{}
			}
			out[k] = applyMergePatch(d, pv)
		default:
			out[k] = p
		}
	}
	return out
}

// sameHidden: a and b agree on what the editor hides.
func sameHidden(a, b map[string]any) bool {
	get := func(o map[string]any, path ...string) any {
		var v any = o
		for _, p := range path {
			m, ok := v.(map[string]any)
			if !ok {
				return nil
			}
			v = m[p]
		}
		return v
	}
	for _, path := range [][]string{{"status"}, {"metadata", "managedFields"}, {"metadata", "annotations", lastAppliedKey}} {
		if !reflect.DeepEqual(get(a, path...), get(b, path...)) {
			return false
		}
	}
	return true
}

type editFx struct {
	warnings    []core.Message
	destructive bool
}

// editEffects: what the edit may cause, from the object read and the
// patch (after: the patch laid over it) — only what they show.
func editEffects(def *kindDef, cur *unstructured.Unstructured, patch, after map[string]any) editFx {
	var fx editFx
	for _, o := range cur.GetOwnerReferences() {
		if o.Controller != nil && *o.Controller {
			fx.warnings = append(fx.warnings, msg("edit.controller", "ownerKind", o.Kind, "owner", o.Name))
		}
	}
	if m := managedBy(cur); m != "" {
		fx.warnings = append(fx.warnings, msg("edit.managedBy", "manager", m))
	}
	spec, _ := patch["spec"].(map[string]any)
	if _, ok := spec["template"]; ok && (def == deploymentsKind || def == statefulSetsKind || def == daemonSetsKind) {
		fx.warnings = append(fx.warnings, msg("edit.rollout"))
	}
	if n, ok := spec["replicas"].(json.Number); ok && n.String() == "0" {
		fx.warnings = append(fx.warnings, msg("edit.replicasZero"))
		fx.destructive = true
	}
	if cur.GetDeletionTimestamp() != nil {
		if gone := missing(cur.GetFinalizers(), strsOf(after, "metadata", "finalizers")); len(gone) > 0 {
			fx.warnings = append(fx.warnings, msg("edit.finalizers", "finalizers", strings.Join(gone, ", ")))
			fx.destructive = true
		}
	}
	if def == configMapsKind {
		var gone []string
		for _, field := range []string{"data", "binaryData"} {
			was, _ := cur.Object[field].(map[string]any)
			now, _ := after[field].(map[string]any)
			for k := range was {
				if _, ok := now[k]; !ok {
					gone = append(gone, k)
				}
			}
		}
		if len(gone) > 0 {
			sort.Strings(gone)
			fx.warnings = append(fx.warnings, msg("edit.dataKeys", "keys", strings.Join(gone, ", ")))
			fx.destructive = true
		}
	}
	for _, path := range [][]string{{"spec", "volumeClaimTemplates"}, {"spec", "template", "spec", "volumes"}, {"spec", "volumes"}} {
		if gone := missing(namesAt(cur.Object, path...), namesAt(after, path...)); len(gone) > 0 {
			fx.warnings = append(fx.warnings, msg("edit.volumes", "names", strings.Join(gone, ", ")))
			fx.destructive = true
		}
	}
	if removed := nullPaths(patch, ""); len(removed) > 0 {
		if len(removed) > 10 {
			removed = append(removed[:10], "…")
		}
		fx.warnings = append(fx.warnings, msg("edit.removes", "paths", strings.Join(removed, ", ")))
	}
	return fx
}

// managedBy names a deployment tool that owns the object, if any.
func managedBy(u *unstructured.Unstructured) string {
	if m := u.GetLabels()["app.kubernetes.io/managed-by"]; m != "" {
		return m
	}
	ann := u.GetAnnotations()
	if r := ann["meta.helm.sh/release-name"]; r != "" {
		return "Helm (release " + r + ")"
	}
	if ann["argocd.argoproj.io/tracking-id"] != "" {
		return "Argo CD"
	}
	return ""
}

// missing: what was has and now does not.
func missing(was, now []string) []string {
	have := map[string]bool{}
	for _, n := range now {
		have[n] = true
	}
	var out []string
	for _, w := range was {
		if !have[w] {
			out = append(out, w)
		}
	}
	return out
}

func strsOf(o map[string]any, path ...string) []string {
	var v any = o
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	l, _ := v.([]any)
	out := make([]string, 0, len(l))
	for _, x := range l {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// namesAt: the names of the list of objects at path.
func namesAt(o map[string]any, path ...string) []string {
	var v any = o
	for _, p := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[p]
	}
	l, _ := v.([]any)
	var out []string
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				out = append(out, n)
			} else if meta, ok := m["metadata"].(map[string]any); ok {
				if n, ok := meta["name"].(string); ok {
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// nullPaths: where the patch removes a key. Sorted.
func nullPaths(patch map[string]any, path string) []string {
	var out []string
	for k, v := range patch {
		at := strings.TrimPrefix(path+"."+k, ".")
		switch t := v.(type) {
		case nil:
			out = append(out, at)
		case map[string]any:
			out = append(out, nullPaths(t, at)...)
		}
	}
	sort.Strings(out)
	return out
}
