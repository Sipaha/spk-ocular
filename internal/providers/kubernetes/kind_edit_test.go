package kubernetes

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// editOf reads the object's text, changes it and reviews the edit, like
// the editor does.
func (c *actionCluster) editOf(ref core.Ref, change func(string) string) (core.EditPlan, *provider.EditGrant, provider.EditRequest) {
	c.t.Helper()
	doc, base, err := c.sess.EditSource(context.Background(), ref)
	require.NoError(c.t, err)
	req := provider.EditRequest{Ref: doc.Ref, Base: base, Original: doc.Text, Edited: change(doc.Text)}
	require.NotEqual(c.t, req.Original, req.Edited, "the change must apply to the text")
	plan, grant, err := c.sess.PrepareEdit(context.Background(), req)
	require.NoError(c.t, err)
	return plan, grant, req
}

func (c *actionCluster) write(req provider.EditRequest, g *provider.EditGrant) (core.EditResult, error) {
	c.t.Helper()
	require.NotNil(c.t, g, "the review grants the write")
	return c.sess.RunEdit(context.Background(), provider.EditRun{EditRequest: req, Grant: *g})
}

func replace(from, to string) func(string) string {
	return func(s string) string { return strings.Replace(s, from, to, 1) }
}

func (c *actionCluster) configMap(name string) core.Ref {
	c.t.Helper()
	c.kubectlNS("create", "configmap", name, "--from-literal=a=1", "--from-literal=b=2")
	return c.ref("configmaps", name)
}

func TestKindEditConfigMapIsCheckedByTheServerAndWrittenOnce(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.configMap("cfg")
	plan, grant, req := c.editOf(ref, replace(`a: "1"`, `a: "5"`))
	assert.True(t, plan.Checked, "a described kind: the server's dry run")
	assert.False(t, plan.Destructive)
	assert.Equal(t, core.RightsAllowed, plan.Rights.State)
	assert.Contains(t, plan.After, `a: "5"`)
	assert.Equal(t, "1", c.jsonpath("configmap", "cfg", "{.data.a}"), "a review writes nothing")

	res, err := c.write(req, grant)
	require.NoError(t, err)
	assert.Equal(t, "5", c.jsonpath("configmap", "cfg", "{.data.a}"))
	assert.Equal(t, res.Version, c.jsonpath("configmap", "cfg", "{.metadata.resourceVersion}"))
	assert.Contains(t, res.Actual, `a: "5"`)
	// The editor's field manager wrote it.
	assert.Contains(t, c.jsonpath("configmap", "cfg", "{.metadata.managedFields[*].manager}"), "spk-ocular")

	// The same grant again: the object moved on — a conflict, nothing written.
	_, err = c.write(req, grant)
	assertClass(t, err, provider.ClassConflict)
}

func TestKindEditDeploymentImageRollsOutAndZeroReplicasIsDestructive(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	plan, grant, req := c.editOf(ref, replace("image: nginx:1.27-alpine", "image: nginx:1.28-alpine"))
	assert.True(t, plan.Checked)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.rollout")
	gen := c.jsonpath("deployment", "web", "{.metadata.generation}")
	_, err := c.write(req, grant)
	require.NoError(t, err)
	assert.Equal(t, "nginx:1.28-alpine", c.jsonpath("deployment", "web", "{.spec.template.spec.containers[0].image}"))
	assert.NotEqual(t, gen, c.jsonpath("deployment", "web", "{.metadata.generation}"))

	plan, _, _ = c.editOf(ref, replace("replicas: 1", "replicas: 0"))
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.replicasZero")
}

func TestKindEditCustomResourceIsProvenAndChecked(t *testing.T) {
	c := kindActionCluster(t)
	require.Eventually(t, func() bool { return c.sess.Catalog().State == core.CatalogReady }, 30*time.Second, 50*time.Millisecond)
	c.apply("apiVersion: ocular.dev/v1\nkind: Widget\nmetadata: {name: w1}\nspec: {size: 3, detail: first}\n")
	ref := c.ref("widgets.ocular.dev", "w1")
	ref.Kind = "ocular.dev/widgets"
	plan, grant, req := c.editOf(ref, replace("size: 3", "size: 4"))
	assert.True(t, plan.Checked, "a CRD's group-version has a Local APIService: dry run proven")
	_, err := c.write(req, grant)
	require.NoError(t, err)
	assert.Equal(t, "4", c.jsonpath("widgets.ocular.dev", "w1", "{.spec.size}"))
}

func TestKindEditSecretChangesOnlyItsMetadata(t *testing.T) {
	c := kindActionCluster(t)
	c.kubectlNS("create", "secret", "generic", "s", "--from-literal=pin=1234")
	ref := c.ref("secrets", "s")
	data := c.jsonpath("secret", "s", "{.data.pin}")
	plan, grant, req := c.editOf(ref, replace("  name: s\n", "  labels:\n    team: a\n  name: s\n"))
	assert.NotContains(t, plan.After, data)
	assert.NotContains(t, plan.Before, data)
	_, err := c.write(req, grant)
	require.NoError(t, err)
	assert.Equal(t, "a", c.jsonpath("secret", "s", "{.metadata.labels.team}"))
	assert.Equal(t, data, c.jsonpath("secret", "s", "{.data.pin}"), "the value is untouched")
}

func TestKindEditChangedMeanwhile(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.configMap("cfg")
	doc, base, err := c.sess.EditSource(context.Background(), ref)
	require.NoError(t, err)
	// Someone changes b after the editor opened.
	c.kubectlNS("patch", "configmap", "cfg", "--type=merge", "-p", `{"data":{"b":"3"}}`)

	// Editing a: laid over the new version, nothing overwritten.
	req := provider.EditRequest{Ref: doc.Ref, Base: base, Original: doc.Text, Edited: strings.Replace(doc.Text, `a: "1"`, `a: "7"`, 1)}
	plan, _, err := c.sess.PrepareEdit(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, plan.Rebased)
	assert.Empty(t, plan.Collisions)
	assert.Contains(t, plan.After, `b: "3"`, "the other change is kept")

	// Editing b too: a collision with what changed meanwhile.
	req.Edited = strings.Replace(doc.Text, `b: "2"`, `b: "9"`, 1)
	plan, grant, err := c.sess.PrepareEdit(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, []string{"data.b"}, plan.Collisions)
	assert.True(t, plan.Destructive)

	// Between the review and the write: a conflict, nothing written.
	c.kubectlNS("patch", "configmap", "cfg", "--type=merge", "-p", `{"data":{"c":"x"}}`)
	_, err = c.write(req, grant)
	assertClass(t, err, provider.ClassConflict)
	assert.Equal(t, "3", c.jsonpath("configmap", "cfg", "{.data.b}"))
}

func TestKindEditRefusalsAreUnavailableWithoutAGrant(t *testing.T) {
	c := kindActionCluster(t)
	ref := c.deployment("web", 1)
	// An immutable field (the selector): the server's 422 at the dry run.
	plan, grant, _ := c.editOf(ref, replace("      app: web\n", "      app: other\n"))
	require.NotNil(t, plan.Unavailable)
	assert.Contains(t, plan.Unavailable.Text, "immutable")
	assert.Nil(t, grant)

	// An unknown field under strict validation.
	cm := c.configMap("cfg")
	plan, grant, _ = c.editOf(cm, func(s string) string { return s + "datta:\n  x: \"1\"\n" })
	require.NotNil(t, plan.Unavailable)
	assert.Contains(t, plan.Unavailable.Text, "unknown field")
	assert.Nil(t, grant)
	assert.Empty(t, c.jsonpath("configmap", "cfg", "{.datta}"))
}

func TestKindEditRemovingTheFinalizersOfADeletingObjectIsDestructive(t *testing.T) {
	c := kindActionCluster(t)
	c.apply("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: held, finalizers: [ocular.dev/hold]}\ndata: {a: \"1\"}\n")
	ref := c.ref("configmaps", "held")
	c.kubectlNS("delete", "configmap", "held", "--wait=false")
	plan, grant, req := c.editOf(ref, replace("  finalizers:\n  - ocular.dev/hold\n", ""))
	assert.True(t, plan.Destructive)
	assert.Contains(t, keys(plan.Warnings), "kubernetes.edit.finalizers")
	_, err := c.write(req, grant)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, err := c.run("-n", c.ns, "get", "configmap", "held")
		return err != nil
	}, 30*time.Second, 200*time.Millisecond, "deleted once its finalizer is gone")
}

func TestKindEditWithoutTheRightIsDenied(t *testing.T) {
	s := rbacSession(t, "viewer").(*session)
	c := &actionCluster{t: t, ns: "ocular-demo"}
	pod := c.kubectl("-n", "ocular-demo", "get", "pods", "-l", "app=web", "-o", "jsonpath={.items[0].metadata.name}")
	ref := core.Ref{Provider: ProviderID, Target: s.target, Scope: "ocular-demo", Kind: "pods", Name: pod}
	doc, base, err := s.EditSource(context.Background(), ref)
	require.NoError(t, err)
	req := provider.EditRequest{Ref: doc.Ref, Base: base, Original: doc.Text, Edited: strings.Replace(doc.Text, "  labels:\n", "  labels:\n    edited: \"yes\"\n", 1)}
	plan, grant, err := s.PrepareEdit(context.Background(), req)
	require.NoError(t, err)
	assert.Nil(t, grant, "no write is granted")
	require.NotNil(t, plan.Unavailable, "the dry run is refused")
	assert.Equal(t, core.RightsDenied, plan.Rights.State)
	assert.Empty(t, c.kubectl("-n", "ocular-demo", "get", "pod", pod, "-o", "jsonpath={.metadata.labels.edited}"))
}
