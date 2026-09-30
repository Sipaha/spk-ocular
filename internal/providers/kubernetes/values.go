package kubernetes

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var _ provider.ValueHolder = (*session)(nil)

// maxValue bounds one value (decoded bytes): the API's limit on a whole
// Secret.
const maxValue = 1 << 20

// maxConsumers bounds the pods a review names.
const maxConsumers = 20

// printKey keys the fingerprints of values and the digests of grants: a
// process's own (a new process: old bases and grants mean nothing).
var printKey = func() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}()

// secretKeyName is what the API server takes as a Secret's key.
var secretKeyName = regexp.MustCompile(`^[-._a-zA-Z0-9]+$`)

// field writes s length-prefixed: no two records join into the same bytes.
func field(h hash.Hash, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	h.Write([]byte(s))
}

// valuePrint is a value's keyed fingerprint (never a plain hash: a short
// value's could be guessed from a decoded base).
func valuePrint(route, uid, key string, b []byte) string {
	m := hmac.New(sha256.New, printKey)
	field(m, "spk-ocular/value-print/v1")
	field(m, route)
	field(m, uid)
	field(m, key)
	field(m, string(b))
	return hex.EncodeToString(m.Sum(nil))
}

// grantDigest binds the final write: where, which object at which
// version, which key, set or delete, whether the key was there when the
// keys were listed, and the bytes.
func grantDigest(route, ns, name, uid, version, key, op string, present bool, b []byte) string {
	m := hmac.New(sha256.New, printKey)
	field(m, "spk-ocular/value-grant/v1")
	for _, f := range []string{route, ns, name, uid, version, key, op, fmt.Sprint(present)} {
		field(m, f)
	}
	field(m, string(b))
	return hex.EncodeToString(m.Sum(nil))
}

// secretData is a Secret's values, decoded. A value the server stored is
// valid base64; one that is not is kept as its raw text (never dropped).
func secretData(u *unstructured.Unstructured) map[string][]byte {
	out := map[string][]byte{}
	data, _, _ := unstructured.NestedMap(u.Object, "data")
	for k, v := range data {
		s, _ := v.(string)
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			b = []byte(s)
		}
		out[k] = b
	}
	return out
}

// isText: shown and edited as text — UTF-8 without control characters but
// \n and \t. A \r is not text: a browser's text field turns CRLF into LF.
func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return false
		}
	}
	return true
}

func prints(route, uid string, data map[string][]byte) []provider.ValuePrint {
	out := make([]provider.ValuePrint, 0, len(data))
	for k, b := range data {
		out = append(out, provider.ValuePrint{Key: k, Present: true, Print: valuePrint(route, uid, k, b)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func printOf(ps []provider.ValuePrint, key string) provider.ValuePrint {
	for _, p := range ps {
		if p.Key == key {
			return p
		}
	}
	return provider.ValuePrint{Key: key}
}

// differing names the keys where a and b disagree (presence or print).
func differing(a, b []provider.ValuePrint) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range append(append([]provider.ValuePrint{}, a...), b...) {
		if seen[p.Key] {
			continue
		}
		seen[p.Key] = true
		if printOf(a, p.Key) != printOf(b, p.Key) {
			out = append(out, p.Key)
		}
	}
	sort.Strings(out)
	return out
}

func (s *session) valueTarget(ref core.Ref) (*kindDef, error) {
	def := s.kind(ref.Kind)
	if def != secretsKind {
		return nil, &provider.Error{Class: provider.ClassUnsupported, Message: fmt.Sprintf("%s keeps no values", ref.Kind)}
	}
	return def, nil
}

// Values lists a Secret's keys with sizes, read now (lists keep no values).
func (s *session) Values(ctx context.Context, ref core.Ref) (core.ValueList, provider.ValueBase, error) {
	def, err := s.valueTarget(ref)
	if err != nil {
		return core.ValueList{}, provider.ValueBase{}, err
	}
	u, err := s.editGet(ctx, def, ref)
	if err != nil {
		return core.ValueList{}, provider.ValueBase{}, err
	}
	data := secretData(u)
	list := core.ValueList{
		Ref:     core.Ref{Provider: ProviderID, Target: s.target, Scope: u.GetNamespace(), Kind: def.desc.ID, Name: u.GetName(), UID: string(u.GetUID())},
		Version: u.GetResourceVersion(),
		Keys:    make([]core.ValueKey, 0, len(data)),
	}
	for k, b := range data {
		list.Keys = append(list.Keys, core.ValueKey{Key: k, Size: len(b), Text: isText(b)})
	}
	sort.Slice(list.Keys, func(i, j int) bool { return list.Keys[i].Key < list.Keys[j].Key })
	route := editRoute(def)
	base := provider.ValueBase{Route: route, Namespace: u.GetNamespace(), Name: u.GetName(), UID: string(u.GetUID()),
		Version: u.GetResourceVersion(), Keys: prints(route, string(u.GetUID()), data)}
	return list, base, nil
}

// RevealValue reads one key's value now, of that object only (its UID).
func (s *session) RevealValue(ctx context.Context, ref core.Ref, key string) (core.Value, error) {
	def, err := s.valueTarget(ref)
	if err != nil {
		return core.Value{}, err
	}
	if ref.UID == "" {
		return core.Value{}, badEdit("which object: its UID is missing")
	}
	u, err := s.editGet(ctx, def, ref)
	if err != nil {
		return core.Value{}, err
	}
	b, ok := secretData(u)[key]
	if !ok {
		return core.Value{}, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("key %q is no longer in secret %s", key, u.GetName())}
	}
	if len(b) > maxValue {
		return core.Value{}, badEdit("the value is over 1 MiB")
	}
	v := core.Value{Key: key, Size: len(b), Text: isText(b), UID: string(u.GetUID()), Version: u.GetResourceVersion()}
	if v.Text {
		v.Value = string(b)
	} else {
		v.Value = base64.StdEncoding.EncodeToString(b)
	}
	return v, nil
}

// valueCheck is a value edit checked against its base.
type valueCheck struct {
	def   *kindDef
	ref   core.Ref
	route string
	// was: the key as the keys were listed.
	was   provider.ValuePrint
	patch map[string]any
}

func (s *session) checkValueEdit(req provider.ValueEditRequest) (*valueCheck, error) {
	def, err := s.valueTarget(req.Ref)
	if err != nil {
		return nil, err
	}
	b := req.Base
	route := editRoute(def)
	if b.Route != route {
		return nil, &provider.Error{Class: provider.ClassConflict, Message: "the API resource of Secrets changed since the keys were listed: open them again"}
	}
	if req.Ref.Name != b.Name || req.Ref.Scope != b.Namespace || (req.Ref.UID != "" && req.Ref.UID != b.UID) || b.UID == "" {
		return nil, badEdit("the object is not the one the keys were listed of")
	}
	if len(req.Key) > 253 || !secretKeyName.MatchString(req.Key) {
		return nil, badEdit("a key is letters, digits, '-', '_' and '.' (at most 253)")
	}
	var patch map[string]any
	switch req.Op {
	case core.ValueSet:
		if len(req.Value) > maxValue {
			return nil, badEdit("the value is over 1 MiB")
		}
		patch = map[string]any{"data": map[string]any{req.Key: base64.StdEncoding.EncodeToString(req.Value)}}
	case core.ValueDelete:
		if len(req.Value) > 0 {
			return nil, badEdit("deleting a key takes no value")
		}
		patch = map[string]any{"data": map[string]any{req.Key: nil}}
	default:
		return nil, badEdit("unknown operation %q", req.Op)
	}
	ref := req.Ref
	ref.UID, ref.Scope, ref.Name = b.UID, b.Namespace, b.Name
	return &valueCheck{def: def, ref: ref, route: route, was: printOf(b.Keys, req.Key), patch: patch}, nil
}

// PrepareValueEdit reads what a key's change would do; nothing changes and
// no value leaves (sizes, key names, fingerprints only).
func (s *session) PrepareValueEdit(ctx context.Context, req provider.ValueEditRequest) (core.ValuePlan, *provider.ValueGrant, error) {
	c, err := s.checkValueEdit(req)
	if err != nil {
		return core.ValuePlan{}, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, prepareEditTimeout)
	defer cancel()
	defer context.AfterFunc(s.ctx, cancel)()
	plan, grant, err := s.prepareValueEdit(ctx, c, req)
	switch {
	case s.ctx.Err() != nil:
		return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the connection was closed during the review: review it again"}
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the review took too long: try again"}
	case ctx.Err() != nil:
		return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassUnavailable, Message: "the review was cancelled"}
	}
	return plan, grant, err
}

func (s *session) prepareValueEdit(ctx context.Context, c *valueCheck, req provider.ValueEditRequest) (core.ValuePlan, *provider.ValueGrant, error) {
	def := c.def
	cur, err := s.editGet(ctx, def, c.ref)
	if err != nil {
		return core.ValuePlan{}, nil, err
	}
	uid := string(cur.GetUID())
	data := secretData(cur)
	now, present := data[req.Key]
	plan := core.ValuePlan{
		Where: core.LiveTarget{Provider: ProviderID, Target: s.conn.target, TargetTitle: s.conn.targetTitle, Endpoint: s.conn.endpoint,
			ConfigHash: s.hash, Ref: c.ref},
		Key: req.Key, Op: req.Op, Before: -1, After: -1,
	}
	if present {
		plan.Before = len(now)
	}
	local := map[string][]byte{}
	for k, b := range data {
		local[k] = b
	}
	if req.Op == core.ValueSet {
		plan.After = len(req.Value)
		plan.Changed = !present || !bytes.Equal(now, req.Value)
		local[req.Key] = req.Value
	} else {
		plan.Changed = present
		delete(local, req.Key)
	}
	plan.Rebased = cur.GetResourceVersion() != req.Base.Version
	nowPrint := provider.ValuePrint{Key: req.Key}
	if present {
		nowPrint = provider.ValuePrint{Key: req.Key, Present: true, Print: valuePrint(c.route, uid, req.Key, now)}
	}
	plan.Collision = nowPrint != c.was
	if b, _, _ := unstructured.NestedBool(cur.Object, "immutable"); b {
		m := msg("values.immutable")
		plan.Unavailable = &m
		return plan, nil, nil
	}
	if !plan.Changed {
		return plan, nil, nil
	}

	send, err := withPreconditions(c.patch, uid, cur.GetResourceVersion())
	if err != nil {
		return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	mode, result := "local", local
	if s.edits.proven(ctx, s.cat.get, def) {
		body, werr := s.editWriter().editPatch(ctx, def.gvr, cur.GetNamespace(), cur.GetName(), send, true)
		switch {
		case werr == nil:
			u := &unstructured.Unstructured{}
			if err := u.UnmarshalJSON(body); err != nil {
				return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassInternal, Message: "the dry run's answer could not be read"}
			}
			result, mode, plan.Checked = secretData(u), "checked", true
		case noDryRun(werr):
			plan.Warnings = append(plan.Warnings, msg("edit.noDryRunWebhook"))
		case apierrors.IsConflict(werr):
			return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassConflict, Message: fmt.Sprintf("secret %s changed while the edit was being checked: review it again", cur.GetName())}
		case apierrors.IsNotFound(werr):
			return core.ValuePlan{}, nil, &provider.Error{Class: provider.ClassGone, Message: fmt.Sprintf("secret %s no longer exists", cur.GetName())}
		case apierrors.IsInvalid(werr) || apierrors.IsBadRequest(werr) || apierrors.IsForbidden(werr):
			m := secretSafe(werr)
			plan.Unavailable = &m
		default:
			class, _ := classify(werr)
			return core.ValuePlan{}, nil, &provider.Error{Class: class, Message: secretSafe(werr).Text}
		}
	} else {
		plan.Warnings = append(plan.Warnings, msg("edit.local"))
	}
	expected := prints(c.route, uid, result)
	plan.ServerChanges = differing(prints(c.route, uid, local), expected)

	if plan.Rebased {
		plan.Warnings = append(plan.Warnings, msg("values.rebased"))
	}
	if plan.Collision {
		plan.Warnings = append(plan.Warnings, msg("values.collision", "key", req.Key))
	}
	if len(plan.ServerChanges) > 0 {
		plan.Warnings = append(plan.Warnings, msg("values.serverChanges", "keys", strings.Join(plan.ServerChanges, ", ")))
	}
	if t := str(cur.Object, "type"); t != "" && t != "Opaque" {
		plan.Warnings = append(plan.Warnings, msg("values.typed", "type", t))
	}
	// Deleting a key is always dangerous: readers outside the pods (a
	// controller, an operator, another system) cannot be seen.
	plan.Destructive = req.Op == core.ValueDelete || plan.Collision || !plan.Checked || len(plan.ServerChanges) > 0
	if req.Op == core.ValueDelete {
		plan.Warnings = append(plan.Warnings, msg("values.delete"))
	}

	ectx, cancel := context.WithTimeout(ctx, prepareExtrasTimeout)
	defer cancel()
	rights := make(chan core.Rights, 1)
	consumers := make(chan consumerScan, 1)
	go func() { rights <- s.rights(ectx, def, "edit", cur) }()
	go func() { consumers <- s.secretConsumers(ectx, cur, req.Key) }()
	for got := 0; got < 2; {
		select {
		case plan.Rights = <-rights:
			rights = nil
			got++
		case sc := <-consumers:
			plan.Consumers = &sc.ValueConsumers
			if sc.requires != "" && req.Op == core.ValueDelete {
				plan.Warnings = append(plan.Warnings, msg("values.deleteUsed", "key", req.Key, "pods", sc.requires))
			}
			if len(sc.Items) > 0 || !sc.Known {
				plan.Warnings = append(plan.Warnings, msg("values.reload"))
			}
			consumers = nil
			got++
		case <-ectx.Done():
			if rights != nil {
				plan.Rights = core.Rights{State: core.RightsUnknown, Reason: "the check took too long"}
			}
			if consumers != nil {
				plan.Consumers = &core.ValueConsumers{Why: "the lookup took too long"}
				plan.Warnings = append(plan.Warnings, msg("values.reload"))
			}
			got = 2
		}
	}
	if plan.Unavailable != nil || plan.Rights.State == core.RightsDenied {
		return plan, nil, nil
	}
	return plan, &provider.ValueGrant{
		Route: c.route, Namespace: cur.GetNamespace(), Name: cur.GetName(), UID: uid, Version: cur.GetResourceVersion(),
		Digest:   grantDigest(c.route, cur.GetNamespace(), cur.GetName(), uid, cur.GetResourceVersion(), req.Key, req.Op, c.was.Present, req.Value),
		Expected: expected, ServerChanges: plan.ServerChanges, Mode: mode,
	}, nil
}

// RunValueEdit writes a reviewed change once: the same key, operation and
// bytes, pinned to the UID and the version the review was made against.
// The answer is checked against the review's expected keys (no value
// leaves).
func (s *session) RunValueEdit(ctx context.Context, run provider.ValueEditRun) (core.ValueResult, error) {
	c, err := s.checkValueEdit(run.ValueEditRequest)
	if err != nil {
		return core.ValueResult{}, err
	}
	g := run.Grant
	if g.Route != c.route {
		return core.ValueResult{}, &provider.Error{Class: provider.ClassConflict, Message: "the API resource of Secrets changed since the edit was reviewed: review it again"}
	}
	if g.UID != run.Base.UID || g.Name != run.Base.Name || g.Namespace != run.Base.Namespace || g.Version == "" {
		return core.ValueResult{}, badEdit("the review is of another object")
	}
	want := grantDigest(c.route, g.Namespace, g.Name, g.UID, g.Version, run.Key, run.Op, c.was.Present, run.Value)
	if !hmac.Equal([]byte(want), []byte(g.Digest)) {
		return core.ValueResult{}, badEdit("the edit is not the one reviewed: review it again")
	}
	send, err := withPreconditions(c.patch, g.UID, g.Version)
	if err != nil {
		return core.ValueResult{}, &provider.Error{Class: provider.ClassInternal, Message: err.Error()}
	}
	name := "secret " + g.Name
	wctx, cancel := context.WithTimeout(ctx, getTimeout)
	defer cancel()
	body, werr := s.editWriter().editPatch(wctx, c.def.gvr, g.Namespace, g.Name, send, false)
	if werr != nil {
		return core.ValueResult{}, editWriteError(werr, name, true)
	}
	done := "written"
	if run.Op == core.ValueDelete {
		done = "deleted"
	}
	out := core.ValueResult{Message: fmt.Sprintf("%s: key %s %s", name, run.Key, done), ServerChanges: g.ServerChanges}
	u := &unstructured.Unstructured{}
	if err := u.UnmarshalJSON(body); err != nil {
		// Written; what the server kept is not known: never "as reviewed".
		out.Differs = []string{run.Key}
		return out, nil
	}
	out.Version = u.GetResourceVersion()
	out.Differs = differing(g.Expected, prints(c.route, string(u.GetUID()), secretData(u)))
	return out, nil
}

// consumerScan is what reads a Secret among the namespace's pods.
type consumerScan struct {
	core.ValueConsumers
	// requires: pods that read the key without marking it optional.
	requires string
}

// secretConsumers looks for the Secret in the specs of the namespace's
// pods (env, envFrom, volumes with their items, projected volumes, image
// pull secrets; all containers: regular, init, ephemeral). It only adds to
// the review: a failed or cut lookup is "not known", never "nobody".
func (s *session) secretConsumers(ctx context.Context, sec *unstructured.Unstructured, key string) consumerScan {
	name := sec.GetName()
	var trunc bool
	type use struct {
		pod      string
		how      map[string]bool
		requires bool
	}
	var uses []use
	_, err := s.list(ctx, podsKind, sec.GetNamespace(), "", &trunc, func(p *unstructured.Unstructured) bool {
		u := use{pod: p.GetName(), how: map[string]bool{}}
		spec := p.Object
		for _, path := range [][]string{{"spec", "containers"}, {"spec", "initContainers"}, {"spec", "ephemeralContainers"}} {
			for _, c := range slice(spec, path...) {
				for _, e := range slice(c, "env") {
					if str(e, "valueFrom", "secretKeyRef", "name") == name && str(e, "valueFrom", "secretKeyRef", "key") == key {
						u.how["env"] = true
						if b, _, _ := unstructured.NestedBool(e, "valueFrom", "secretKeyRef", "optional"); !b {
							u.requires = true
						}
					}
				}
				for _, e := range slice(c, "envFrom") {
					if str(e, "secretRef", "name") == name {
						u.how["envFrom"] = true
					}
				}
			}
		}
		items := func(src map[string]any) {
			its := slice(src, "items")
			if len(its) == 0 {
				u.how["volume"] = true
				return
			}
			for _, it := range its {
				if str(it, "key") == key {
					u.how["volume"] = true
					if b, _, _ := unstructured.NestedBool(src, "optional"); !b {
						u.requires = true
					}
				}
			}
		}
		for _, v := range slice(spec, "spec", "volumes") {
			if str(v, "secret", "secretName") == name {
				m, _, _ := unstructured.NestedMap(v, "secret")
				items(m)
			}
			for _, src := range slice(v, "projected", "sources") {
				if str(src, "secret", "name") == name {
					m, _, _ := unstructured.NestedMap(src, "secret")
					items(m)
				}
			}
		}
		for _, r := range slice(spec, "spec", "imagePullSecrets") {
			if str(r, "name") == name {
				u.how["imagePull"] = true
			}
		}
		if len(u.how) == 0 {
			return false
		}
		uses = append(uses, u)
		return true
	})
	out := consumerScan{ValueConsumers: core.ValueConsumers{Known: err == nil && !trunc}}
	switch {
	case err != nil:
		out.Why = err.Error()
	case trunc:
		out.Why = "more pods than were looked at"
	}
	var requires []string
	for i, u := range uses {
		if u.requires {
			requires = append(requires, u.pod)
		}
		if i >= maxConsumers {
			out.Known = false
			if out.Why == "" {
				out.Why = fmt.Sprintf("more than %d pods", maxConsumers)
			}
			continue
		}
		how := make([]string, 0, len(u.how))
		for h := range u.how {
			how = append(how, h)
		}
		sort.Strings(how)
		out.Items = append(out.Items, fmt.Sprintf("pods/%s (%s)", u.pod, strings.Join(how, ", ")))
	}
	sort.Strings(requires)
	out.requires = strings.Join(requires, ", ")
	return out
}
