package kubernetes

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func configValueRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "ctx", Kind: "configmaps", Scope: "ns", Name: "cfg", UID: "uid-cfg"}
}
func configValuesObject() *unstructured.Unstructured {
	u := configMap("cfg", "7", map[string]any{"app.yaml": "port: 8080\n", "keep": "unchanged", "empty": "", "crlf": "line\r\n"}, nil)
	u.Object["binaryData"] = map[string]any{"blob": b64("\x00\xff"), "binary-text": b64("text in binaryData")}
	return u
}
func TestConfigMapValuesListAndRevealBothStorageFields(t *testing.T) {
	s, _ := valueSession(t, configValuesObject())
	list, base, err := s.Values(context.Background(), configValueRef())
	require.NoError(t, err)
	require.Len(t, list.Keys, 6)
	require.Len(t, base.Keys, 6)
	for _, kind := range newKindRegistry(configMapsKind, secretsKind).descriptors() {
		assert.True(t, kind.Values)
	}
	for _, tc := range []struct {
		key, value string
		text       bool
	}{{"app.yaml", "port: 8080\n", true}, {"empty", "", true}, {"crlf", b64("line\r\n"), false}, {"blob", b64("\x00\xff"), false}, {"binary-text", "text in binaryData", true}} {
		got, err := s.RevealValue(context.Background(), configValueRef(), tc.key)
		require.NoError(t, err)
		assert.Equal(t, tc.value, got.Value)
		assert.Equal(t, tc.text, got.Text)
	}
	assert.NotContains(t, mustJSON(t, list), "port: 8080")
	assert.NotContains(t, mustJSON(t, base), "text in binaryData")
}
func TestConfigMapValueEditPreservesOtherKeysAndBinaryStorage(t *testing.T) {
	for _, tc := range []struct{ key, value, field string }{{"app.yaml", "port: 9090\n", "data"}, {"crlf", "line two\r\n", "data"}, {"blob", "\x00\x01\xff", "binaryData"}, {"binary-text", "new text", "binaryData"}, {"new-file", "hello", "data"}, {"new-empty", "", "data"}, {"new-binary", "\xff", "binaryData"}, {"empty", "", "data"}} {
		t.Run(tc.key, func(t *testing.T) {
			s, w := valueSession(t, configValuesObject())
			_, base, err := s.Values(context.Background(), configValueRef())
			require.NoError(t, err)
			req := provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: tc.key, Op: core.ValueSet, Value: []byte(tc.value)}
			plan, grant, err := s.PrepareValueEdit(context.Background(), req)
			require.NoError(t, err)
			if tc.key == "empty" {
				assert.False(t, plan.Changed)
				assert.Nil(t, grant)
				return
			}
			require.NotNil(t, grant)
			assert.True(t, plan.Checked)
			assert.Empty(t, w.writes(false))
			result, err := s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: req, Grant: *grant})
			require.NoError(t, err)
			assert.Empty(t, result.Differs)
			require.Len(t, w.writes(false), 1)
			patch := w.writes(false)[0].body
			assert.Equal(t, "uid-cfg", patch["metadata"].(map[string]any)["uid"])
			assert.Equal(t, "7", patch["metadata"].(map[string]any)["resourceVersion"])
			expected := tc.value
			if tc.field == "binaryData" {
				expected = base64.StdEncoding.EncodeToString([]byte(tc.value))
			}
			assert.Equal(t, map[string]any{tc.key: expected}, patch[tc.field])
			object, err := w.dyn.Resource(cmGVR).Namespace("ns").Get(context.Background(), "cfg", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, "unchanged", str(object.Object, "data", "keep"))
			if tc.key != "blob" {
				assert.Equal(t, b64("\x00\xff"), str(object.Object, "binaryData", "blob"))
			}
		})
	}
}
func TestConfigMapValueEditRefusesImmutableReplacedAndChangedObjects(t *testing.T) {
	t.Run("immutable", func(t *testing.T) {
		u := configValuesObject()
		u.Object["immutable"] = true
		s, _ := valueSession(t, u)
		_, base, err := s.Values(context.Background(), configValueRef())
		require.NoError(t, err)
		plan, grant, err := s.PrepareValueEdit(context.Background(), provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: "keep", Op: core.ValueSet, Value: []byte("new")})
		require.NoError(t, err)
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.values.configMapImmutable", plan.Unavailable.Key)
		assert.Nil(t, grant)
	})
	for _, replacement := range []bool{false, true} {
		t.Run(map[bool]string{false: "version changed", true: "UID replaced"}[replacement], func(t *testing.T) {
			s, w := valueSession(t, configValuesObject())
			_, base, err := s.Values(context.Background(), configValueRef())
			require.NoError(t, err)
			req := provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: "keep", Op: core.ValueSet, Value: []byte("new")}
			_, grant, err := s.PrepareValueEdit(context.Background(), req)
			require.NoError(t, err)
			require.NotNil(t, grant)
			u := configValuesObject()
			u.SetResourceVersion("8")
			if replacement {
				u.SetUID("replacement")
			}
			_, err = w.dyn.Resource(cmGVR).Namespace("ns").Update(context.Background(), u, metav1.UpdateOptions{})
			require.NoError(t, err)
			_, err = s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: req, Grant: *grant})
			require.Error(t, err)
			assert.Empty(t, w.writes(false))
		})
	}
}
func TestConfigMapValueDeleteAndConsumersUseConfigMapReferences(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "reader", "namespace": "ns"}, "spec": map[string]any{"containers": []any{map[string]any{"name": "main", "env": []any{map[string]any{"name": "PORT", "valueFrom": map[string]any{"configMapKeyRef": map[string]any{"name": "cfg", "key": "app.yaml"}}}}}}}}}
	spec := pod.Object["spec"].(map[string]any)
	spec["containers"].([]any)[0].(map[string]any)["envFrom"] = []any{map[string]any{"configMapRef": map[string]any{"name": "cfg"}}}
	spec["volumes"] = []any{
		map[string]any{"name": "direct", "configMap": map[string]any{"name": "cfg", "items": []any{map[string]any{"key": "app.yaml", "path": "application"}}}},
		map[string]any{"name": "projected", "projected": map[string]any{"sources": []any{map[string]any{"configMap": map[string]any{"name": "cfg"}}}}},
	}
	secretReader := pod.DeepCopy()
	secretReader.SetName("secret-reader")
	secretReader.Object["spec"] = map[string]any{"containers": []any{map[string]any{"name": "main", "envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "cfg"}}}}}, "volumes": []any{map[string]any{"name": "secret", "secret": map[string]any{"secretName": "cfg"}}}, "imagePullSecrets": []any{map[string]any{"name": "cfg"}}}
	s, w := valueSession(t, configValuesObject(), pod, secretReader)
	_, base, err := s.Values(context.Background(), configValueRef())
	require.NoError(t, err)
	req := provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: "app.yaml", Op: core.ValueDelete}
	plan, grant, err := s.PrepareValueEdit(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, grant)
	require.NotNil(t, plan.Consumers)
	assert.Equal(t, []string{"pods/reader (env, envFrom, volume)"}, plan.Consumers.Items)
	assert.True(t, plan.Destructive)
	_, err = s.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: req, Grant: *grant})
	require.NoError(t, err)
	u, err := w.dyn.Resource(cmGVR).Namespace("ns").Get(context.Background(), "cfg", metav1.GetOptions{})
	require.NoError(t, err)
	_, found, _ := unstructured.NestedFieldNoCopy(u.Object, "data", "app.yaml")
	assert.False(t, found)
	assert.Equal(t, "unchanged", str(u.Object, "data", "keep"))
	assert.Equal(t, b64("\x00\xff"), str(u.Object, "binaryData", "blob"))
}

func TestConfigMapReviewTextDiffUsesExpectedResult(t *testing.T) {
	for _, tc := range []struct {
		name, key, value, after string
		op                      string
		binary                  bool
	}{
		{name: "server admission", key: "app.yaml", value: "typed", after: "admitted\n", op: core.ValueSet},
		{name: "add", key: "new", value: "hello", after: "hello", op: core.ValueSet},
		{name: "delete", key: "app.yaml", op: core.ValueDelete},
		{name: "empty", key: "app.yaml", op: core.ValueSet},
		{name: "final LF", key: "app.yaml", value: "port: 8080", after: "port: 8080", op: core.ValueSet},
		{name: "binary before", key: "blob", value: "text", after: "text", op: core.ValueSet, binary: true},
		{name: "binary after", key: "app.yaml", value: "text", after: "\x00\xff", op: core.ValueSet, binary: true},
		{name: "CRLF", key: "crlf", value: "text", after: "text", op: core.ValueSet, binary: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := valueSession(t, configValuesObject())
			_, base, err := s.Values(context.Background(), configValueRef())
			require.NoError(t, err)
			w.dryAnswer = func(o map[string]any) {
				if tc.op == core.ValueDelete {
					delete(o["data"].(map[string]any), tc.key)
				} else if tc.key != "blob" {
					if tc.name == "binary after" {
						delete(o["data"].(map[string]any), tc.key)
						o["binaryData"].(map[string]any)[tc.key] = b64(tc.after)
					} else {
						o["data"].(map[string]any)[tc.key] = tc.after
					}
				}
			}
			plan, grant, err := s.PrepareValueEdit(context.Background(), provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: tc.key, Op: tc.op, Value: []byte(tc.value)})
			require.NoError(t, err)
			require.NotNil(t, grant)
			require.True(t, plan.Checked)
			assert.Empty(t, w.writes(false))
			if tc.binary {
				assert.Nil(t, plan.TextDiff)
				return
			}
			require.NotNil(t, plan.TextDiff)
			before := "port: 8080\n"
			if tc.key == "new" {
				before = ""
			}
			assert.Equal(t, before, plan.TextDiff.Before)
			assert.Equal(t, tc.after, plan.TextDiff.After)
			assert.NotContains(t, mustJSON(t, grant), "port: 8080")
		})
	}
}

func TestConfigMapReviewDiffRebasesOnFreshValue(t *testing.T) {
	s, w := valueSession(t, configValuesObject())
	_, base, err := s.Values(context.Background(), configValueRef())
	require.NoError(t, err)
	updated := configValuesObject()
	updated.SetResourceVersion("8")
	updated.Object["data"].(map[string]any)["app.yaml"] = "concurrent\n"
	_, err = w.dyn.Resource(cmGVR).Namespace("ns").Update(context.Background(), updated, metav1.UpdateOptions{})
	require.NoError(t, err)
	plan, _, err := s.PrepareValueEdit(context.Background(), provider.ValueEditRequest{Ref: configValueRef(), Base: base, Key: "app.yaml", Op: core.ValueSet, Value: []byte("draft\n")})
	require.NoError(t, err)
	require.NotNil(t, plan.TextDiff)
	assert.True(t, plan.Rebased)
	assert.True(t, plan.Collision)
	assert.Equal(t, "concurrent\n", plan.TextDiff.Before)
	assert.Equal(t, "draft\n", plan.TextDiff.After)
}
