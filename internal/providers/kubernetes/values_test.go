package kubernetes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var secretGVR = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// marker is a value no answer but RevealValue may carry, in either form.
const marker = "marker-7c1f"

var marker64 = base64.StdEncoding.EncodeToString([]byte(marker))

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func secret(rv string, data map[string]string, extra map[string]any) *unstructured.Unstructured {
	d := map[string]any{}
	for k, v := range data {
		d[k] = b64(v)
	}
	o := map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata": map[string]any{"name": "s", "namespace": "ns", "uid": "uid-s", "resourceVersion": rv},
		"data":     d}
	for k, v := range extra {
		o[k] = v
	}
	return &unstructured.Unstructured{Object: o}
}

func valueClient(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		secretGVR: "SecretList", podGVR: "PodList",
	}, objs...)
}

func valueSession(t *testing.T, objs ...runtime.Object) (*session, *editRec) {
	t.Helper()
	c := valueClient(objs...)
	s := newSession("ctx", "h", c, false)
	t.Cleanup(s.Close)
	w := &editRec{dyn: c}
	s.writer = w
	return s, w
}

var secRef = core.Ref{Provider: ProviderID, Target: "ctx", Scope: "ns", Kind: "secrets", Name: "s"}

func listed(t *testing.T, s *session) (core.ValueList, provider.ValueBase) {
	t.Helper()
	l, b, err := s.Values(context.Background(), secRef)
	require.NoError(t, err)
	return l, b
}

func setReq(b provider.ValueBase, key, value string) provider.ValueEditRequest {
	return provider.ValueEditRequest{Ref: secRef, Base: b, Key: key, Op: core.ValueSet, Value: []byte(value)}
}

func delReq(b provider.ValueBase, key string) provider.ValueEditRequest {
	return provider.ValueEditRequest{Ref: secRef, Base: b, Key: key, Op: core.ValueDelete}
}

// noMarker: nothing JSON-encoded carries the marker, plain or base64.
func noMarker(t *testing.T, what string, vs ...any) {
	t.Helper()
	for _, v := range vs {
		b, err := json.Marshal(v)
		require.NoError(t, err)
		if e, ok := v.(error); ok && e != nil {
			b = append(b, e.Error()...)
		}
		assert.NotContains(t, string(b), marker, what)
		assert.NotContains(t, string(b), marker64, what)
	}
}

func change(t *testing.T, w *editRec, u *unstructured.Unstructured) {
	t.Helper()
	_, err := w.dyn.Resource(secretGVR).Namespace("ns").Update(context.Background(), u, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func TestValuesListKeysAndSizesNeverValues(t *testing.T) {
	s, _ := valueSession(t, secret("3", map[string]string{"token": marker, "crlf": "a\r\nb", "nul": "a\x00b", "text": "héllo\n\tok", "empty": ""}, nil))
	l, b := listed(t, s)
	assert.Equal(t, "uid-s", l.Ref.UID)
	assert.Equal(t, "3", l.Version)
	assert.Equal(t, []core.ValueKey{{Key: "crlf", Size: 4}, {Key: "empty", Size: 0, Text: true}, {Key: "nul", Size: 3}, {Key: "text", Size: 10, Text: true}, {Key: "token", Size: len(marker), Text: true}}, l.Keys)
	assert.Len(t, b.Keys, 5)
	for _, p := range b.Keys {
		assert.True(t, p.Present)
		assert.Len(t, p.Print, 64)
	}
	noMarker(t, "list and base", l, b)
}

func TestRevealValueIsOneKeyOfThatObject(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"token": marker, "bin": "a\x00\xff"}, nil))
	ref := secRef
	ref.UID = "uid-s"
	v, err := s.RevealValue(context.Background(), ref, "token")
	require.NoError(t, err)
	assert.Equal(t, core.Value{Key: "token", Size: len(marker), Text: true, Value: marker, UID: "uid-s", Version: "3"}, v)
	v, err = s.RevealValue(context.Background(), ref, "bin")
	require.NoError(t, err)
	assert.Equal(t, core.Value{Key: "bin", Size: 3, Value: base64.StdEncoding.EncodeToString([]byte("a\x00\xff")), UID: "uid-s", Version: "3"}, v)

	_, err = s.RevealValue(context.Background(), ref, "nope")
	assertClass(t, err, provider.ClassGone)
	_, err = s.RevealValue(context.Background(), secRef, "token")
	assertClass(t, err, provider.ClassInvalid) // no UID: which object?
	other := secret("4", map[string]string{"token": "x"}, nil)
	other.SetUID("uid-new")
	change(t, w, other)
	_, err = s.RevealValue(context.Background(), ref, "token")
	assertClass(t, err, provider.ClassGone) // replaced under the same name
}

func TestValueReadRefusalsAreHidden(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"token": marker}, nil))
	ref := secRef
	ref.UID = "uid-s"
	_, b := listed(t, s)
	w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "s", errors.New("webhook says "+marker+" "+marker64))
	})
	_, _, err := s.Values(context.Background(), secRef)
	assertClass(t, err, provider.ClassForbidden)
	noMarker(t, "values", err)
	_, err = s.RevealValue(context.Background(), ref, "token")
	noMarker(t, "reveal", err)
	_, _, err = s.PrepareValueEdit(context.Background(), setReq(b, "token", "x"))
	noMarker(t, "prepare", err)
}

func TestValueEditReviewsThenWritesOnceWithPreconditions(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"token": marker, "other": "b"}, nil))
	_, b := listed(t, s)
	plan, grant, err := s.PrepareValueEdit(context.Background(), setReq(b, "token", "new-value"))
	require.NoError(t, err)
	require.NotNil(t, grant)
	assert.True(t, plan.Checked, "a Secret's route is proven: a server dry run")
	assert.True(t, plan.Changed)
	assert.Equal(t, len(marker), plan.Before)
	assert.Equal(t, 9, plan.After)
	assert.False(t, plan.Rebased)
	assert.False(t, plan.Collision)
	assert.False(t, plan.Destructive)
	assert.Empty(t, plan.ServerChanges)
	noMarker(t, "plan and grant", plan, grant)
	assert.NotContains(t, mustJSON(t, grant), "new-value")
	assert.NotContains(t, mustJSON(t, grant), b64("new-value"))
	require.Len(t, w.writes(true), 1)
	assert.Empty(t, w.writes(false), "a review writes nothing")

	res, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: setReq(b, "token", "new-value"), Grant: *grant})
	require.NoError(t, err)
	assert.Empty(t, res.Differs)
	assert.Equal(t, "secret s: key token written", res.Message)
	wet := w.writes(false)
	require.Len(t, wet, 1)
	meta := wet[0].body["metadata"].(map[string]any)
	assert.Equal(t, "uid-s", meta["uid"])
	assert.Equal(t, "3", meta["resourceVersion"])
	assert.Equal(t, map[string]any{"token": b64("new-value")}, wet[0].body["data"])
	noMarker(t, "result", res)
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// The grant binds the final write: operation, key, bytes, object, version.
func TestValueGrantBindsTheWholeWrite(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"a": "x", "b": "y"}, nil))
	_, b := listed(t, s)
	_, setEmpty, err := s.PrepareValueEdit(context.Background(), setReq(b, "a", ""))
	require.NoError(t, err)
	require.NotNil(t, setEmpty)
	_, del, err := s.PrepareValueEdit(context.Background(), delReq(b, "a"))
	require.NoError(t, err)
	require.NotNil(t, del)
	moved := *setEmpty
	moved.Version = "4"
	for name, run := range map[string]provider.ValueEditRun{
		"set-empty grant, delete run":  {ValueEditRequest: delReq(b, "a"), Grant: *setEmpty},
		"delete grant, set-empty run":  {ValueEditRequest: setReq(b, "a", ""), Grant: *del},
		"another key":                  {ValueEditRequest: setReq(b, "b", ""), Grant: *setEmpty},
		"other bytes":                  {ValueEditRequest: setReq(b, "a", "z"), Grant: *setEmpty},
		"another version in the grant": {ValueEditRequest: setReq(b, "a", ""), Grant: moved},
	} {
		_, err := s.RunValueEdit(context.Background(), run)
		assert.Error(t, err, name)
		assertClass(t, err, provider.ClassInvalid)
	}
	assert.Empty(t, w.writes(false), "refused before any write")
}

// Collisions come from the listed keys' fingerprints: no value was shown.
func TestValueCollisionsFromTheListedKeys(t *testing.T) {
	cases := map[string]struct {
		before, after map[string]string
		req           func(provider.ValueBase) provider.ValueEditRequest
		collision     bool
	}{
		"same length change":      {map[string]string{"a": "xx"}, map[string]string{"a": "yy"}, func(b provider.ValueBase) provider.ValueEditRequest { return setReq(b, "a", "zz") }, true},
		"another key changed":     {map[string]string{"a": "x", "b": "1"}, map[string]string{"a": "x", "b": "2"}, func(b provider.ValueBase) provider.ValueEditRequest { return setReq(b, "a", "z") }, false},
		"absent, then created":    {map[string]string{"a": "x"}, map[string]string{"a": "x", "n": "q"}, func(b provider.ValueBase) provider.ValueEditRequest { return setReq(b, "n", "z") }, true},
		"empty, then deleted":     {map[string]string{"a": ""}, map[string]string{}, func(b provider.ValueBase) provider.ValueEditRequest { return setReq(b, "a", "z") }, true},
		"deleted, then recreated": {map[string]string{"a": "x"}, map[string]string{}, func(b provider.ValueBase) provider.ValueEditRequest { return setReq(b, "a", "z") }, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, w := valueSession(t, secret("3", tc.before, nil))
			_, b := listed(t, s)
			if name == "deleted, then recreated" {
				change(t, w, secret("4", map[string]string{}, nil))
				change(t, w, secret("5", map[string]string{"a": "y"}, nil))
			} else {
				change(t, w, secret("4", tc.after, nil))
			}
			plan, _, err := s.PrepareValueEdit(context.Background(), tc.req(b))
			require.NoError(t, err)
			assert.True(t, plan.Rebased)
			assert.Equal(t, tc.collision, plan.Collision)
			assert.Equal(t, tc.collision, plan.Destructive)
			if tc.collision {
				assert.Contains(t, keys(plan.Warnings), "kubernetes.values.collision")
			}
		})
	}
}

// The write is checked against the review (not the listed keys).
func TestValueWriteIsCheckedAgainstTheReview(t *testing.T) {
	t.Run("another key changed before the review: no difference", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a", "other": "b"}, nil))
		_, b := listed(t, s)
		change(t, w, secret("4", map[string]string{"k": "a", "other": "c"}, nil))
		plan, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "d"))
		require.NoError(t, err)
		assert.Empty(t, plan.ServerChanges)
		res, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: setReq(b, "k", "d"), Grant: *g})
		require.NoError(t, err)
		assert.Empty(t, res.Differs)
	})
	t.Run("the dry run changes what was typed: said, dangerous, repeated", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a", "other": "b"}, nil))
		_, b := listed(t, s)
		w.dryAnswer = func(o map[string]any) { o["data"].(map[string]any)["k"] = b64("D") }
		w.wetAnswer = func(o map[string]any) { o["data"].(map[string]any)["k"] = b64("D") }
		plan, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "d"))
		require.NoError(t, err)
		assert.Equal(t, []string{"k"}, plan.ServerChanges)
		assert.True(t, plan.Destructive)
		assert.Contains(t, keys(plan.Warnings), "kubernetes.values.serverChanges")
		res, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: setReq(b, "k", "d"), Grant: *g})
		require.NoError(t, err)
		assert.Empty(t, res.Differs, "written as reviewed")
		assert.Equal(t, []string{"k"}, res.ServerChanges)
	})
	t.Run("the write differs from the review at the same length", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a", "other": "b"}, nil))
		_, b := listed(t, s)
		w.wetAnswer = func(o map[string]any) { o["data"].(map[string]any)["k"] = b64("e") }
		_, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "d"))
		require.NoError(t, err)
		res, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: setReq(b, "k", "d"), Grant: *g})
		require.NoError(t, err)
		assert.Equal(t, []string{"k"}, res.Differs)
	})
	t.Run("another key added or deleted by the write", func(t *testing.T) {
		s, w := valueSession(t, secret("3", map[string]string{"k": "a", "other": "b"}, nil))
		_, b := listed(t, s)
		w.wetAnswer = func(o map[string]any) {
			d := o["data"].(map[string]any)
			delete(d, "other")
			d["added"] = b64("x")
		}
		_, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "d"))
		require.NoError(t, err)
		res, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: setReq(b, "k", "d"), Grant: *g})
		require.NoError(t, err)
		assert.Equal(t, []string{"added", "other"}, res.Differs)
	})
}

func TestValueEditOfAnImmutableSecretIsUnavailable(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, map[string]any{"immutable": true}))
	_, b := listed(t, s)
	plan, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
	require.NoError(t, err)
	assert.Nil(t, g)
	require.NotNil(t, plan.Unavailable)
	assert.Equal(t, "kubernetes.values.immutable", plan.Unavailable.Key)
	assert.Empty(t, w.writes(true), "nothing asked of the server")
}

func TestValueEditRefusedByTheServerIsSaidWithoutItsStrings(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
	_, b := listed(t, s)
	w.fail = func(bool) error {
		return apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, "s", nil)
	}
	w.fail = func(bool) error {
		return &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 422, Reason: metav1.StatusReasonInvalid,
			Message: "denied: " + marker, Details: &metav1.StatusDetails{Causes: []metav1.StatusCause{{Type: metav1.CauseTypeFieldValueInvalid, Field: "data[" + marker64 + "]", Message: marker}}}}}
	}
	plan, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
	require.NoError(t, err)
	assert.Nil(t, g)
	require.NotNil(t, plan.Unavailable)
	noMarker(t, "refused review", plan)
}

func TestValueNoChangeNoGrant(t *testing.T) {
	s, _ := valueSession(t, secret("3", map[string]string{"k": "same"}, nil))
	_, b := listed(t, s)
	plan, g, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "same"))
	require.NoError(t, err)
	assert.False(t, plan.Changed)
	assert.Nil(t, g)
	plan, g, err = s.PrepareValueEdit(context.Background(), delReq(b, "absent"))
	require.NoError(t, err)
	assert.False(t, plan.Changed)
	assert.Nil(t, g)
}

func TestValueEditChecksItsInput(t *testing.T) {
	s, _ := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
	_, b := listed(t, s)
	for name, req := range map[string]provider.ValueEditRequest{
		"bad key":        setReq(b, "a/b", "x"),
		"long key":       setReq(b, strings.Repeat("k", 254), "x"),
		"too big":        setReq(b, "k", strings.Repeat("x", maxValue+1)),
		"delete a value": {Ref: secRef, Base: b, Key: "k", Op: core.ValueDelete, Value: []byte("x")},
		"unknown op":     {Ref: secRef, Base: b, Key: "k", Op: "merge"},
	} {
		_, _, err := s.PrepareValueEdit(context.Background(), req)
		assert.Error(t, err, name)
		assertClass(t, err, provider.ClassInvalid)
	}
	other := b
	other.Name = "t"
	_, _, err := s.PrepareValueEdit(context.Background(), setReq(other, "k", "x"))
	assertClass(t, err, provider.ClassInvalid)
}

func specPod(name string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": "uid-" + name}, "spec": spec}}
}

func TestDeletingAKeyIsAlwaysDangerousAndNamesItsReaders(t *testing.T) {
	keyRef := func(key string, optional bool) map[string]any {
		return map[string]any{"containers": []any{map[string]any{"name": "c", "env": []any{map[string]any{"name": "E",
			"valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "s", "key": key, "optional": optional}}}}}}}
	}
	s, _ := valueSession(t, secret("3", map[string]string{"k": "a", "j": "b"}, nil),
		specPod("reader", keyRef("k", false)),
		specPod("optional", keyRef("k", true)),
		specPod("init", map[string]any{"containers": []any{}, "initContainers": []any{map[string]any{"name": "i", "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "s"}}}}}}),
		specPod("items", map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "v", "secret": map[string]any{"secretName": "s", "items": []any{map[string]any{"key": "k", "path": "p"}}}}}}),
		specPod("other-items", map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "v", "secret": map[string]any{"secretName": "s", "items": []any{map[string]any{"key": "j", "path": "p"}}}}}}),
		specPod("projected", map[string]any{"containers": []any{}, "volumes": []any{map[string]any{"name": "v", "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": "s"}}}}}}}),
		specPod("unrelated", map[string]any{"containers": []any{map[string]any{"name": "c"}}}),
	)
	_, b := listed(t, s)
	plan, g, err := s.PrepareValueEdit(context.Background(), delReq(b, "k"))
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.True(t, plan.Destructive)
	require.NotNil(t, plan.Consumers)
	assert.True(t, plan.Consumers.Known)
	assert.ElementsMatch(t, []string{"pods/reader (env)", "pods/optional (env)", "pods/init (envFrom)", "pods/items (volume)", "pods/projected (volume)"}, plan.Consumers.Items)
	w := keys(plan.Warnings)
	assert.Contains(t, w, "kubernetes.values.delete")
	assert.Contains(t, w, "kubernetes.values.deleteUsed")
	for _, m := range plan.Warnings {
		if m.Key == "kubernetes.values.deleteUsed" {
			assert.Equal(t, "items, reader", m.Params["pods"])
		}
	}

	// Nobody seen reading it: deleting is dangerous all the same.
	s, _ = valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
	_, b = listed(t, s)
	plan, _, err = s.PrepareValueEdit(context.Background(), delReq(b, "k"))
	require.NoError(t, err)
	assert.True(t, plan.Destructive)
	assert.True(t, plan.Consumers.Known)
	assert.Empty(t, plan.Consumers.Items)
}

func TestConsumersNotListedAreUnknownNotNone(t *testing.T) {
	s, w := valueSession(t, secret("3", map[string]string{"k": "a"}, nil))
	w.dyn.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("no"))
	})
	_, b := listed(t, s)
	plan, _, err := s.PrepareValueEdit(context.Background(), setReq(b, "k", "z"))
	require.NoError(t, err)
	require.NotNil(t, plan.Consumers)
	assert.False(t, plan.Consumers.Known)
	assert.NotEmpty(t, plan.Consumers.Why)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.values.reload")
}
