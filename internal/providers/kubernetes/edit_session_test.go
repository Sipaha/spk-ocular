package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type editCall struct {
	dry  bool
	name string
	body map[string]any
}

// editRec records edit writes; a dry run is answered by laying the patch
// over the stored object (409 on another version), a write by the fake
// client. fail, if set, answers instead.
type editRec struct {
	dyn   dynamic.Interface
	mu    sync.Mutex
	calls []editCall
	fail  func(dry bool) error
	// dryAnswer, if set, edits the dry run's answer (server-side changes).
	dryAnswer func(o map[string]any)
	// hold, if set, runs first with the request's context (a slow server).
	hold func(ctx context.Context) error
}

func (w *editRec) patch(context.Context, schema.GroupVersionResource, string, string, types.PatchType, []byte, string) error {
	return errors.New("unexpected action write")
}

func (w *editRec) delete(context.Context, schema.GroupVersionResource, string, string, metav1.DeleteOptions) error {
	return errors.New("unexpected delete")
}

func (w *editRec) editPatch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, data []byte, dry bool) ([]byte, error) {
	var body map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.calls = append(w.calls, editCall{dry: dry, name: name, body: body})
	fail, dryAnswer, hold := w.fail, w.dryAnswer, w.hold
	w.mu.Unlock()
	if hold != nil {
		if err := hold(ctx); err != nil {
			return nil, err
		}
	}
	if fail != nil {
		if err := fail(dry); err != nil {
			return nil, err
		}
	}
	cur, err := w.dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	meta := body["metadata"].(map[string]any)
	if meta["resourceVersion"] != cur.GetResourceVersion() || meta["uid"] != string(cur.GetUID()) {
		return nil, apierrors.NewConflict(schema.GroupResource{Group: gvr.Group, Resource: gvr.Resource}, name, errors.New("the object has been modified"))
	}
	if !dry {
		u, err := w.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, types.MergePatchType, data, metav1.PatchOptions{})
		if err != nil {
			return nil, err
		}
		return u.MarshalJSON()
	}
	full, err := asTree(cur.Object)
	if err != nil {
		return nil, err
	}
	out := applyMergePatch(full, body)
	if dryAnswer != nil {
		dryAnswer(out)
	}
	return json.Marshal(out)
}

func (w *editRec) writes(dry bool) []editCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []editCall
	for _, c := range w.calls {
		if c.dry == dry {
			out = append(out, c)
		}
	}
	return out
}

func configMap(name, rv string, data map[string]any, meta map[string]any) *unstructured.Unstructured {
	m := map[string]any{"name": name, "namespace": "ns", "uid": "uid-" + name, "resourceVersion": rv}
	for k, v := range meta {
		m[k] = v
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": m, "data": data}}
}

var cmGVR = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}

func editClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		cmGVR: "ConfigMapList", {Version: "v1", Resource: "secrets"}: "SecretList", deployGVR: "DeploymentList",
	}, objs...)
}

func editSession(t *testing.T, objs ...runtime.Object) (*session, *editRec) {
	t.Helper()
	c := editClient(objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	w := &editRec{dyn: c}
	s.writer = w
	return s, w
}

func cmRef(name string) core.Ref {
	return core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "configmaps", Name: name}
}

// source reads the editor's text and base.
func source(t *testing.T, s *session, ref core.Ref) (core.EditDoc, provider.EditBase) {
	t.Helper()
	doc, base, err := s.EditSource(context.Background(), ref)
	require.NoError(t, err)
	return doc, base
}

func request(doc core.EditDoc, base provider.EditBase, edited string) provider.EditRequest {
	return provider.EditRequest{Ref: doc.Ref, Base: base, Original: doc.Text, Edited: edited}
}

func TestEditSourceShowsTheObjectAndPinsItsVersion(t *testing.T) {
	s, _ := editSession(t, configMap("cfg", "7", map[string]any{"a": "1"}, map[string]any{
		"managedFields": []any{map[string]any{"manager": "kubectl"}},
		"annotations":   map[string]any{lastAppliedKey: "{}"},
	}))
	doc, base := source(t, s, cmRef("cfg"))
	assert.Equal(t, "uid-cfg", doc.Ref.UID)
	assert.Equal(t, "7", doc.Version, "the live row's revision form")
	assert.NotContains(t, doc.Text, "managedFields:")
	assert.NotContains(t, doc.Text, lastAppliedKey)
	assert.Equal(t, provider.EditBase{Route: editRoute(configMapsKind), APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns", Name: "cfg", UID: "uid-cfg",
		Version: "7", DocHash: sha([]byte(doc.Text)), Format: editFormat}, base)
	_, _, err := s.EditSource(context.Background(), core.Ref{Provider: ProviderID, Target: "ctx", Kind: "events", Name: "x"})
	assert.Error(t, err, "events are not edited")
}

func TestPrepareEditAsksADryRunAndGrantsTheVersionReviewed(t *testing.T) {
	s, w := editSession(t, configMap("cfg", "7", map[string]any{"a": "1", "b": "2"}, nil))
	doc, base := source(t, s, cmRef("cfg"))
	plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "a: \"1\"", "a: \"9\"", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Checked)
	assert.True(t, plan.Changed)
	assert.False(t, plan.Rebased)
	assert.Contains(t, plan.Before, `a: "1"`)
	assert.Contains(t, plan.After, `a: "9"`)
	require.Len(t, w.writes(true), 1)
	assert.Empty(t, w.writes(false), "a review writes nothing")
	sent := w.writes(true)[0].body
	assert.Equal(t, map[string]any{"uid": "uid-cfg", "resourceVersion": "7"}, sent["metadata"])
	assert.Equal(t, map[string]any{"a": "9"}, sent["data"])
	require.NotNil(t, grant)
	assert.Equal(t, "7", grant.Version)
	assert.Equal(t, "checked", grant.Mode)

	// No change: nothing to write, nothing asked.
	plan, grant, err = s.PrepareEdit(context.Background(), request(doc, base, doc.Text))
	require.NoError(t, err)
	assert.False(t, plan.Changed)
	assert.Nil(t, grant)
	assert.Len(t, w.writes(true), 1)
}

func TestPrepareEditOfAnUnprovenRouteSendsNothing(t *testing.T) {
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	editable := v2ver{version: "v1", res: []v2res{{name: "widgets", kind: "Widget", singular: "widget", scope: "Namespaced", verbs: append(append([]string{}, lw...), "patch")}}}
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {editable}}, "ocular.dev"))
	api.set("/apis/apiregistration.k8s.io/v1/apiservices/v1.ocular.dev", []byte(`{"apiVersion":"apiregistration.k8s.io/v1","kind":"APIService","metadata":{"name":"v1.ocular.dev"},"spec":{"group":"ocular.dev","version":"v1","service":{"name":"agg","namespace":"x"}}}`))
	api.set("/openapi/v3/apis/ocular.dev/v1", []byte(`{"paths":{"/apis/ocular.dev/v1/namespaces/{namespace}/widgets/{name}":{"patch":{"parameters":[{"name":"force","in":"query"}]}}}}`))
	c := catalogClient(widget("w1"))
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	w := &editRec{dyn: c}
	s.writer = w
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)

	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "ocular.dev/widgets", Name: "w1"}
	doc, base := source(t, s, ref)
	plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, doc.Text+"spec: {size: 3}\n"))
	require.NoError(t, err)
	assert.Empty(t, w.calls, "no mutating request without a proven dry run")
	assert.False(t, plan.Checked)
	assert.True(t, plan.Destructive)
	assert.Contains(t, plan.After, "size: 3")
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.local")
	require.NotNil(t, grant)
	assert.Equal(t, "local", grant.Mode)
}

func keys(ms []core.Message) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Key)
	}
	return out
}

func TestPrepareEditOverAChangedObjectSaysWhatItOverwrites(t *testing.T) {
	s, w := editSession(t, configMap("cfg", "7", map[string]any{"a": "1", "b": "2"}, nil))
	doc, base := source(t, s, cmRef("cfg"))
	// Someone changes b (and adds c) after the editor opened.
	_, err := w.dyn.Resource(cmGVR).Namespace("ns").Update(context.Background(), configMap("cfg", "8", map[string]any{"a": "1", "b": "3", "c": "x"}, nil), metav1.UpdateOptions{})
	require.NoError(t, err)

	plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "a: \"1\"", "a: \"5\"", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Rebased)
	assert.Empty(t, plan.Collisions, "a harmless rebase")
	assert.False(t, plan.Destructive)
	assert.Contains(t, plan.After, `b: "3"`, "the other change is kept")
	assert.Equal(t, grant.Version, w.writes(true)[0].body["metadata"].(map[string]any)["resourceVersion"])

	plan, _, err = s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "b: \"2\"", "b: \"4\"", 1)))
	require.NoError(t, err)
	assert.Equal(t, []string{"data.b"}, plan.Collisions)
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.collisions")
}

func TestPrepareEditKeepsHiddenFieldsAndLetsTheServerRewriteItsOwn(t *testing.T) {
	s, w := editSession(t, configMap("cfg", "7", map[string]any{"a": "1"}, map[string]any{
		"managedFields": []any{map[string]any{"manager": "kubectl"}},
		"annotations":   map[string]any{lastAppliedKey: "{}", "team": "a"},
	}))
	// The server rewrites managedFields at every write: not a refusal.
	w.dryAnswer = func(o map[string]any) {
		o["metadata"].(map[string]any)["managedFields"] = []any{map[string]any{"manager": "spk-ocular"}}
	}
	doc, base := source(t, s, cmRef("cfg"))
	plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "  annotations:\n    team: a\n", "", 1)))
	require.NoError(t, err)
	require.NotNil(t, grant)
	assert.True(t, plan.Checked)
	sent := w.writes(true)[0].body["metadata"].(map[string]any)
	assert.Equal(t, map[string]any{"team": nil}, sent["annotations"], "only the shown annotation is removed")
}

func TestPrepareEditRefusalsAndFallbacks(t *testing.T) {
	cm := func() runtime.Object { return configMap("cfg", "7", map[string]any{"a": "1"}, nil) }
	edited := func(doc core.EditDoc) string { return strings.Replace(doc.Text, "a: \"1\"", "a: \"2\"", 1) }

	t.Run("invalid is unavailable, without a grant", func(t *testing.T) {
		s, w := editSession(t, cm())
		w.fail = func(bool) error {
			return apierrors.NewInvalid(schema.GroupKind{Kind: "ConfigMap"}, "cfg", nil)
		}
		doc, base := source(t, s, cmRef("cfg"))
		plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
		require.NoError(t, err)
		require.NotNil(t, plan.Unavailable)
		assert.Nil(t, grant)
	})
	t.Run("a webhook without dry run falls back to local", func(t *testing.T) {
		s, w := editSession(t, cm())
		w.fail = func(bool) error {
			return apierrors.NewBadRequest(`admission webhook "x.io" does not support dry run`)
		}
		doc, base := source(t, s, cmRef("cfg"))
		plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
		require.NoError(t, err)
		assert.False(t, plan.Checked)
		assert.True(t, plan.Destructive)
		assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.noDryRunWebhook")
		require.NotNil(t, grant)
		assert.Equal(t, "local", grant.Mode)
	})
	t.Run("a webhook's denial that speaks of dry run is not a fallback", func(t *testing.T) {
		for _, text := range []string{
			`admission webhook "x.io" denied the request: this object does not support dry run`,
			`admission webhook "x.io" denied the request: admission webhook "y.io" does not support dry run`,
			`policy: does not support dry run`,
		} {
			s, w := editSession(t, cm())
			w.fail = func(bool) error { return apierrors.NewBadRequest(text) }
			doc, base := source(t, s, cmRef("cfg"))
			plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
			require.NoError(t, err)
			require.NotNil(t, plan.Unavailable, text)
			assert.Nil(t, grant, text)
		}
	})
	t.Run("another bad request is not a fallback", func(t *testing.T) {
		s, w := editSession(t, cm())
		w.fail = func(bool) error { return apierrors.NewBadRequest("strict decoding error: unknown field \"datta\"") }
		doc, base := source(t, s, cmRef("cfg"))
		plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
		require.NoError(t, err)
		require.NotNil(t, plan.Unavailable)
		assert.Contains(t, plan.Unavailable.Text, "unknown field")
		assert.Nil(t, grant)
	})
	t.Run("changed while checking is a conflict", func(t *testing.T) {
		s, w := editSession(t, cm())
		w.fail = func(bool) error {
			return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "cfg", errors.New("modified"))
		}
		doc, base := source(t, s, cmRef("cfg"))
		_, _, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
		assertClass(t, err, provider.ClassConflict)
	})
	t.Run("a server failure is an error, not a plan", func(t *testing.T) {
		s, w := editSession(t, cm())
		w.fail = func(bool) error { return apierrors.NewInternalError(errors.New("boom")) }
		doc, base := source(t, s, cmRef("cfg"))
		_, grant, err := s.PrepareEdit(context.Background(), request(doc, base, edited(doc)))
		require.Error(t, err)
		assert.Nil(t, grant)
	})
	t.Run("forged texts are refused before anything is asked", func(t *testing.T) {
		s, w := editSession(t, cm())
		doc, base := source(t, s, cmRef("cfg"))
		forged := request(doc, base, edited(doc))
		forged.Original = strings.Replace(doc.Text, "a: \"1\"", "a: \"0\"", 1)
		_, _, err := s.PrepareEdit(context.Background(), forged)
		assertClass(t, err, provider.ClassInvalid)
		other := request(doc, base, strings.Replace(doc.Text, "name: cfg", "name: other", 1))
		_, _, err = s.PrepareEdit(context.Background(), other)
		assertClass(t, err, provider.ClassInvalid)
		assert.Empty(t, w.calls)
	})
}

func run(req provider.EditRequest, g *provider.EditGrant) provider.EditRun {
	return provider.EditRun{EditRequest: req, Grant: *g}
}

func TestRunEditWritesTheReviewedPatchOnceAtTheReviewedVersion(t *testing.T) {
	s, w := editSession(t, configMap("cfg", "7", map[string]any{"a": "1"}, nil))
	doc, base := source(t, s, cmRef("cfg"))
	req := request(doc, base, strings.Replace(doc.Text, "a: \"1\"", "a: \"2\"", 1))
	_, grant, err := s.PrepareEdit(context.Background(), req)
	require.NoError(t, err)

	// A different edit than the reviewed one is refused, nothing written.
	tampered := req
	tampered.Edited = strings.Replace(doc.Text, "a: \"1\"", "a: \"3\"", 1)
	_, err = s.RunEdit(context.Background(), run(tampered, grant))
	assertClass(t, err, provider.ClassInvalid)
	assert.Empty(t, w.writes(false))

	res, err := s.RunEdit(context.Background(), run(req, grant))
	require.NoError(t, err)
	require.Len(t, w.writes(false), 1)
	assert.Equal(t, "7", w.writes(false)[0].body["metadata"].(map[string]any)["resourceVersion"])
	assert.Contains(t, res.Actual, `a: "2"`)

	// The same grant again: the object moved on (8) — a conflict, not a
	// write at the newer version, and no retry. (The fake client keeps
	// versions as they are: move it.)
	_, err = w.dyn.Resource(cmGVR).Namespace("ns").Update(context.Background(), configMap("cfg", "8", map[string]any{"a": "2"}, nil), metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = s.RunEdit(context.Background(), run(req, grant))
	assertClass(t, err, provider.ClassConflict)
	assert.Len(t, w.writes(false), 2)
}

func TestRunEditOutcomesAreNeverRetried(t *testing.T) {
	for name, tc := range map[string]struct {
		err   error
		class provider.ErrorClass
	}{
		"server error": {apierrors.NewInternalError(errors.New("boom")), provider.ClassUnknown},
		"no answer":    {errors.New("connection reset"), provider.ClassUnknown},
		// An answer is not a local credential failure, whatever it says.
		"server error naming credentials": {apierrors.NewInternalError(errors.New("getting credentials: vault down")), provider.ClassUnknown},
		"credential plugin failed":        {errors.New(`getting credentials: exec: executable kubelogin not found`), provider.ClassUnauthorized},
		"invalid":                         {apierrors.NewInvalid(schema.GroupKind{Kind: "ConfigMap"}, "cfg", nil), provider.ClassInvalid},
		"forbidden":                       {apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "cfg", errors.New("no")), provider.ClassForbidden},
		"gone":                            {apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "cfg"), provider.ClassGone},
	} {
		t.Run(name, func(t *testing.T) {
			s, w := editSession(t, configMap("cfg", "7", map[string]any{"a": "1"}, nil))
			doc, base := source(t, s, cmRef("cfg"))
			req := request(doc, base, strings.Replace(doc.Text, "a: \"1\"", "a: \"2\"", 1))
			_, grant, err := s.PrepareEdit(context.Background(), req)
			require.NoError(t, err)
			w.fail = func(dry bool) error {
				if dry {
					return nil
				}
				return tc.err
			}
			_, err = s.RunEdit(context.Background(), run(req, grant))
			assertClass(t, err, tc.class)
			assert.Len(t, w.writes(false), 1)
		})
	}
}

func TestASecretIsEditedOnlyInItsMetadataAndItsRefusalsAreHidden(t *testing.T) {
	sec := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{"name": "s", "namespace": "ns", "uid": "uid-s", "resourceVersion": "3"},
		"data":     map[string]any{"pin": "MTIz"}}}
	c := editClient(sec)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	w := &editRec{dyn: c}
	s.writer = w
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "secrets", Name: "s"}
	doc, base := source(t, s, ref)
	assert.NotContains(t, doc.Text, "MTIz")

	_, _, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "pin: <3 bytes>", "pin: NDU2", 1)))
	assertClass(t, err, provider.ClassInvalid)

	labelled := strings.Replace(doc.Text, "  name: s\n", "  labels:\n    team: a\n  name: s\n", 1)
	w.fail = func(bool) error {
		return &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 422, Reason: "PIN 123 is weak", Message: "PIN 123 is weak"}}
	}
	plan, grant, err := s.PrepareEdit(context.Background(), request(doc, base, labelled))
	require.NoError(t, err)
	require.NotNil(t, plan.Unavailable)
	assert.NotContains(t, plan.Unavailable.Text, "123")
	assert.Nil(t, grant)

	w.fail = nil
	req := request(doc, base, labelled)
	_, grant, err = s.PrepareEdit(context.Background(), req)
	require.NoError(t, err)
	w.fail = func(dry bool) error {
		if dry {
			return nil
		}
		return apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "s", errors.New("webhook: PIN 123 is weak"))
	}
	_, err = s.RunEdit(context.Background(), run(req, grant))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "123")

	// An answer naming credentials is still the server's: hidden, unknown.
	w.fail = func(dry bool) error {
		if dry {
			return nil
		}
		return apierrors.NewInternalError(errors.New("getting credentials: PIN 123"))
	}
	_, err = s.RunEdit(context.Background(), run(req, grant))
	assertClass(t, err, provider.ClassUnknown)
	assert.NotContains(t, err.Error(), "123")
}

func TestASecretsReadRefusalsAreHidden(t *testing.T) {
	sec := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{"name": "s", "namespace": "ns", "uid": "uid-s", "resourceVersion": "3"},
		"data":     map[string]any{"pin": "MTIz"}}}
	c := editClient(sec)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	s.writer = &editRec{dyn: c}
	ref := core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "secrets", Name: "s"}
	doc, base := source(t, s, ref)
	labelled := strings.Replace(doc.Text, "  name: s\n", "  labels:\n    team: a\n  name: s\n", 1)

	c.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "s", errors.New("webhook: PIN 123 is weak"))
	})
	_, _, err := s.EditSource(context.Background(), ref)
	assertClass(t, err, provider.ClassForbidden)
	assert.NotContains(t, err.Error(), "123")
	_, _, err = s.PrepareEdit(context.Background(), request(doc, base, labelled))
	assertClass(t, err, provider.ClassForbidden)
	assert.NotContains(t, err.Error(), "123")
}

func TestEditEffectsThatCannotBeUndone(t *testing.T) {
	now := metav1.Now()
	deleting := configMap("cfg", "7", map[string]any{"a": "1", "b": "2"}, map[string]any{"finalizers": []any{"x.io/keep"}})
	deleting.SetDeletionTimestamp(&now)
	s, _ := editSession(t, deleting)
	doc, base := source(t, s, cmRef("cfg"))
	plan, _, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "  finalizers:\n  - x.io/keep\n", "", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.finalizers")

	s, _ = editSession(t, configMap("cfg", "7", map[string]any{"a": "1", "b": "2"}, nil))
	doc, base = source(t, s, cmRef("cfg"))
	plan, _, err = s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "  b: \"2\"\n", "", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.dataKeys")

	s, _ = editSession(t, workload("Deployment", "web", "uid-web", "4", map[string]any{"replicas": int64(3)}))
	doc, base = source(t, s, deployWebRef)
	plan, _, err = s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "replicas: 3", "replicas: 0", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.replicasZero")
	plan, _, err = s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "team: a", "team: b", 1)))
	require.NoError(t, err)
	assert.False(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.rollout")
}

func TestEditKeepsNumbersExactToTheRequestAndStatusChurnIsNoCollision(t *testing.T) {
	d := workload("Deployment", "web", "uid-web", "4", map[string]any{"replicas": int64(3), "big": int64(9007199254740993)})
	s, w := editSession(t, d)
	doc, base := source(t, s, deployWebRef)
	require.Contains(t, doc.Text, "big: 9007199254740993")
	// Only the status changed since the editor opened (a controller).
	moved := d.DeepCopy()
	moved.SetResourceVersion("5")
	moved.Object["status"] = map[string]any{"readyReplicas": int64(3)}
	_, err := w.dyn.Resource(deployGVR).Namespace("ns").Update(context.Background(), moved, metav1.UpdateOptions{})
	require.NoError(t, err)

	plan, _, err := s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "big: 9007199254740993", "big: 9007199254740995", 1)))
	require.NoError(t, err)
	assert.True(t, plan.Rebased)
	assert.Empty(t, plan.Collisions, "status is not the editor's")
	sent := w.writes(true)[0].body["spec"].(map[string]any)
	assert.Equal(t, json.Number("9007199254740995"), sent["big"])

	// A decimal literal too: as written, not through float64.
	_, _, err = s.PrepareEdit(context.Background(), request(doc, base, strings.Replace(doc.Text, "big: 9007199254740993", "big: 9007199254740993.0", 1)))
	require.NoError(t, err)
	sent = w.writes(true)[1].body["spec"].(map[string]any)
	assert.Equal(t, json.Number("9007199254740993.0"), sent["big"])
}

// holdUntilDone: a server that answers only when the request is given up.
func holdUntilDone(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestPrepareEditIsBoundedAndEndsWithTheSession(t *testing.T) {
	cm := configMap("cfg", "7", map[string]any{"a": "1"}, nil)
	edited := func(doc core.EditDoc) string { return strings.Replace(doc.Text, "a: \"1\"", "a: \"2\"", 1) }
	prepare := func(s *session, req provider.EditRequest) <-chan error {
		done := make(chan error, 1)
		go func() {
			_, grant, err := s.PrepareEdit(context.Background(), req)
			if err == nil && grant != nil {
				done <- errors.New("a grant")
				return
			}
			done <- err
		}()
		return done
	}
	wait := func(t *testing.T, done <-chan error) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("the review did not end")
			return nil
		}
	}

	t.Run("a deadline", func(t *testing.T) {
		old := prepareEditTimeout
		prepareEditTimeout = 50 * time.Millisecond
		t.Cleanup(func() { prepareEditTimeout = old })
		s, w := editSession(t, cm.DeepCopy())
		w.hold = holdUntilDone
		doc, base := source(t, s, cmRef("cfg"))
		err := wait(t, prepare(s, request(doc, base, edited(doc))))
		assertClass(t, err, provider.ClassUnavailable)
	})
	t.Run("the session closes", func(t *testing.T) {
		s, w := editSession(t, cm.DeepCopy())
		started := make(chan struct{})
		w.hold = func(ctx context.Context) error {
			close(started)
			return holdUntilDone(ctx)
		}
		doc, base := source(t, s, cmRef("cfg"))
		done := prepare(s, request(doc, base, edited(doc)))
		<-started
		s.cancel()
		assertClass(t, wait(t, done), provider.ClassUnavailable)
	})
	t.Run("no grant after the session closed", func(t *testing.T) {
		s, w := editSession(t, cm.DeepCopy())
		// The dry run answers, but the session ends meanwhile.
		w.hold = func(context.Context) error {
			s.cancel()
			return nil
		}
		doc, base := source(t, s, cmRef("cfg"))
		assertClass(t, wait(t, prepare(s, request(doc, base, edited(doc)))), provider.ClassUnavailable)
	})
}
