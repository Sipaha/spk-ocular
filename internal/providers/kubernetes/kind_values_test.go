package kubernetes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// kindMarker is a value that must leave the provider only in RevealValue.
const kindMarker = "MARKER-kind-9c2e-value"

// secretWith creates a generic Secret: pin (text, the marker) and bin
// (bytes that are not text: NUL, 0xff, CRLF).
func (c *actionCluster) secretWith(name string) core.Ref {
	c.t.Helper()
	bin := filepath.Join(c.t.TempDir(), "bin")
	require.NoError(c.t, os.WriteFile(bin, []byte{0, 0xff, '\r', '\n', 'x'}, 0o600))
	c.kubectlNS("create", "secret", "generic", name, "--from-literal=pin="+kindMarker, "--from-file=bin="+bin)
	return c.ref("secrets", name)
}

func (c *actionCluster) valueOf(name, key string) []byte {
	c.t.Helper()
	b, err := base64.StdEncoding.DecodeString(c.jsonpath("secret", name, "{.data."+strings.ReplaceAll(key, ".", `\.`)+"}"))
	require.NoError(c.t, err)
	return b
}

// valueEdit lists the keys and reviews a key's change, like the UI does.
func (c *actionCluster) valueEdit(ref core.Ref, key, op string, value []byte) (core.ValuePlan, *provider.ValueGrant, provider.ValueEditRequest) {
	c.t.Helper()
	_, base, err := c.sess.Values(context.Background(), ref)
	require.NoError(c.t, err)
	return c.reviewValue(ref, base, key, op, value)
}

func (c *actionCluster) reviewValue(ref core.Ref, base provider.ValueBase, key, op string, value []byte) (core.ValuePlan, *provider.ValueGrant, provider.ValueEditRequest) {
	c.t.Helper()
	req := provider.ValueEditRequest{Ref: ref, Base: base, Key: key, Op: op, Value: value}
	plan, grant, err := c.sess.PrepareValueEdit(context.Background(), req)
	require.NoError(c.t, err)
	return plan, grant, req
}

func (c *actionCluster) writeValue(req provider.ValueEditRequest, g *provider.ValueGrant) (core.ValueResult, error) {
	c.t.Helper()
	require.NotNil(c.t, g, "the review grants the write")
	return c.sess.RunValueEdit(context.Background(), provider.ValueEditRun{ValueEditRequest: req, Grant: *g})
}

func TestKindValuesShowAndChangeAKeyByteForByte(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.secretWith("s")
	list, _, err := c.sess.Values(context.Background(), ref)
	require.NoError(t, err)
	assert.Equal(t, []core.ValueKey{{Key: "bin", Size: 5}, {Key: "pin", Size: len(kindMarker), Text: true}}, list.Keys)
	assert.Equal(t, ref.UID, list.Ref.UID)

	v, err := c.sess.RevealValue(context.Background(), ref, "pin")
	require.NoError(t, err)
	assert.Equal(t, kindMarker, v.Value)
	assert.Equal(t, list.Version, v.Version)
	v, err = c.sess.RevealValue(context.Background(), ref, "bin")
	require.NoError(t, err)
	assert.False(t, v.Text)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte{0, 0xff, '\r', '\n', 'x'}), v.Value)

	want := []byte("new\r\nvalue\n")
	plan, grant, req := c.valueEdit(ref, "pin", core.ValueSet, want)
	assert.True(t, plan.Checked, "a Secret's route is proven: the server checks the change")
	assert.True(t, plan.Changed)
	assert.Equal(t, len(kindMarker), plan.Before)
	assert.Equal(t, len(want), plan.After)
	assert.False(t, plan.Destructive)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	res, err := c.writeValue(req, grant)
	require.NoError(t, err)
	assert.Empty(t, res.Differs)
	assert.Equal(t, want, c.valueOf("s", "pin"), "the bytes are kept exactly (CRLF too)")
	assert.Equal(t, []byte{0, 0xff, '\r', '\n', 'x'}, c.valueOf("s", "bin"), "the other key is untouched")

	// The grant is spent: its version is gone, the write is a conflict.
	_, err = c.writeValue(req, grant)
	assertClass(t, err, provider.ClassConflict)
}

func TestKindValuesAddAndDeleteAKey(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.secretWith("s")
	plan, grant, req := c.valueEdit(ref, "api.token", core.ValueSet, []byte{})
	assert.Equal(t, -1, plan.Before)
	assert.Equal(t, 0, plan.After)
	_, err := c.writeValue(req, grant)
	require.NoError(t, err)
	assert.Equal(t, "", c.jsonpath("secret", "s", `{.data.api\.token}`))
	assert.Contains(t, c.jsonpath("secret", "s", "{.data}"), "api.token", "an empty value is a value")

	plan, grant, req = c.valueEdit(ref, "pin", core.ValueDelete, nil)
	assert.True(t, plan.Destructive, "deleting a key is always dangerous")
	assert.Equal(t, -1, plan.After)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.values.delete")
	_, err = c.writeValue(req, grant)
	require.NoError(t, err)
	assert.NotContains(t, c.jsonpath("secret", "s", "{.data}"), `"pin"`)
}

func TestKindValuesOfAnImmutableSecretAreUnavailable(t *testing.T) {
	c := kindActionCluster(t)
	c.apply("apiVersion: v1\nkind: Secret\nmetadata: {name: frozen}\nimmutable: true\ndata: {pin: " + base64.StdEncoding.EncodeToString([]byte("1234")) + "}\n")
	ref := c.ref("secrets", "frozen")
	plan, grant, _ := c.valueEdit(ref, "pin", core.ValueSet, []byte("9"))
	require.NotNil(t, plan.Unavailable)
	assert.Equal(t, "kubernetes.values.immutable", plan.Unavailable.Key)
	assert.Nil(t, grant)
}

func TestKindValuesTheServerRefusesATLSSecretWithoutItsKey(t *testing.T) {
	c := kindActionCluster(t)
	b := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	c.apply("apiVersion: v1\nkind: Secret\nmetadata: {name: tls}\ntype: kubernetes.io/tls\ndata: {tls.crt: " + b(kindMarker) + ", tls.key: " + b(kindMarker) + "}\n")
	ref := c.ref("secrets", "tls")
	plan, grant, _ := c.valueEdit(ref, "tls.key", core.ValueDelete, nil)
	require.NotNil(t, plan.Unavailable, "the dry run is refused: a TLS Secret needs tls.key")
	assert.Nil(t, grant)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.values.typed")
	assert.NotContains(t, plan.Unavailable.Text, kindMarker)
	assert.Equal(t, []byte(kindMarker), c.valueOf("tls", "tls.key"), "nothing written")
}

func TestKindValuesChangedMeanwhile(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.secretWith("s")
	_, base, err := c.sess.Values(context.Background(), ref)
	require.NoError(t, err)

	// Another key changed after the keys were listed: rebased only.
	c.kubectlNS("patch", "secret", "s", "--type=merge", "-p", `{"stringData":{"bin":"other"}}`)
	plan, _, _ := c.reviewValue(ref, base, "pin", core.ValueSet, []byte("7"))
	assert.True(t, plan.Rebased)
	assert.False(t, plan.Collision)

	// The key itself changed (same length): a collision, dangerous.
	same := strings.Repeat("z", len(kindMarker))
	c.kubectlNS("patch", "secret", "s", "--type=merge", "-p", `{"stringData":{"pin":"`+same+`"}}`)
	plan, grant, req := c.reviewValue(ref, base, "pin", core.ValueSet, []byte("7"))
	assert.True(t, plan.Collision)
	assert.True(t, plan.Destructive)

	// Changed again between the review and the write: a conflict, nothing written.
	c.kubectlNS("patch", "secret", "s", "--type=merge", "-p", `{"stringData":{"other":"x"}}`)
	_, err = c.writeValue(req, grant)
	assertClass(t, err, provider.ClassConflict)
	assert.Equal(t, []byte(same), c.valueOf("s", "pin"))
}

func TestKindValuesNameThePodsThatReadThem(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.secretWith("s")
	c.apply(`apiVersion: v1
kind: Pod
metadata: {name: reader}
spec:
  containers:
  - name: app
    image: nginx:1.27-alpine
    env:
    - name: PIN
      valueFrom: {secretKeyRef: {name: s, key: pin}}
`)
	plan, _, _ := c.valueEdit(ref, "pin", core.ValueDelete, nil)
	require.NotNil(t, plan.Consumers)
	assert.True(t, plan.Consumers.Known)
	assert.Equal(t, []string{"pods/reader (env)"}, plan.Consumers.Items)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.values.deleteUsed")
	assert.Contains(t, keys(plan.Warnings), "kubernetes.values.reload")
}

func TestKindValuesAViewerWithoutSecretsSeesNoServerText(t *testing.T) {
	s := rbacSession(t, "viewer").(*session)
	ref := core.Ref{Provider: ProviderID, Target: s.target, Scope: "ocular-demo", Kind: "secrets", Name: "db", UID: "u"}
	_, _, err := s.Values(context.Background(), ref)
	assertClass(t, err, provider.ClassForbidden)
	assert.NotContains(t, err.Error(), "system:serviceaccount", "the server's text is never shown for a Secret")
	_, err = s.RevealValue(context.Background(), ref, "pin")
	assertClass(t, err, provider.ClassForbidden)
	assert.NotContains(t, err.Error(), "system:serviceaccount")
}

// The value leaves only in RevealValue: not in the list, base, plan,
// grant, result or any error on the way (a refusal, a conflict).
func TestKindValuesTheMarkerLeavesOnlyInReveal(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.secretWith("s")
	var out []string
	see := func(vs ...any) {
		for _, v := range vs {
			if err, ok := v.(error); ok && err != nil {
				out = append(out, err.Error())
				continue
			}
			b, err := json.Marshal(v)
			require.NoError(t, err)
			out = append(out, string(b))
		}
	}
	list, base, err := c.sess.Values(context.Background(), ref)
	see(list, base, err)
	res, err := c.sess.Get(context.Background(), ref)
	see(res, err)
	plan, grant, req := c.valueEdit(ref, "pin", core.ValueSet, []byte(kindMarker+"2"))
	see(plan, grant)
	wr, err := c.writeValue(req, grant)
	see(wr, err)
	// A write on a spent grant: a conflict.
	_, err = c.writeValue(req, grant)
	see(err)
	// A key the server refuses (the name is not one it takes).
	_, _, err = c.sess.PrepareValueEdit(context.Background(), provider.ValueEditRequest{Ref: ref, Base: base, Key: "a/b", Op: core.ValueSet, Value: []byte(kindMarker)})
	see(err)

	b64 := base64.StdEncoding.EncodeToString([]byte(kindMarker))
	for _, s := range out {
		assert.NotContains(t, s, kindMarker)
		assert.NotContains(t, s, b64)
	}
}
