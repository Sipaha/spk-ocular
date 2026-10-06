package helm

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

func testConfig() *action.Configuration {
	a := action.NewConfiguration(action.ConfigurationSetLogger(slog.NewTextHandler(io.Discard, nil)))
	a.Releases = storage.Init(driver.NewMemory())
	a.KubeClient = &fake.PrintingKubeClient{Out: io.Discard}
	a.Capabilities = common.DefaultCapabilities
	return a
}
func testChart() *chartv2.Chart {
	return &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "demo", Version: "1.0.0", AppVersion: "1"}, Values: map[string]any{"greeting": "hello"}, Templates: []*common.File{{Name: "templates/configmap.yaml", Data: []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\ndata:\n  greeting: {{ .Values.greeting | quote }}\n")}}}
}
func TestLifecycleWithHelmSDK(t *testing.T) {
	a := testConfig()
	f := func(context.Context, string) (*action.Configuration, error) { return a, nil }
	p := NewPlans()
	o := Operation{Action: "install", Name: "demo", Namespace: "test", TimeoutSeconds: 30, Values: "greeting: first\n"}
	plan, err := p.Prepare(t.Context(), "session1", f, o, testChart())
	require.NoError(t, err)
	require.Contains(t, plan.Manifest, "first")
	rows, err := List(a)
	require.NoError(t, err)
	require.Empty(t, rows, "prepare must not write")
	result, err := p.Run(t.Context(), "session1", plan.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome)
	_, err = p.Run(t.Context(), "session1", plan.ID, f)
	require.Error(t, err, "plans are one-shot")
	d, err := Get(a, "demo", 0)
	require.NoError(t, err)
	require.Equal(t, 1, d.Revision)
	require.Contains(t, d.Values, "first")
	o.Action = "upgrade"
	o.Values = "greeting: second\n"
	plan, err = p.Prepare(t.Context(), "session1", f, o, testChart())
	require.NoError(t, err)
	require.Contains(t, plan.PreviousManifest, "first")
	require.Contains(t, plan.Manifest, "second")
	result, err = p.Run(t.Context(), "session1", plan.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome)
	o.Action = "rollback"
	o.Revision = 1
	plan, err = p.Prepare(t.Context(), "session1", f, o, nil)
	require.NoError(t, err)
	result, err = p.Run(t.Context(), "session1", plan.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome)
	d, err = Get(a, "demo", 0)
	require.NoError(t, err)
	require.Equal(t, 3, d.Revision)
	require.Contains(t, d.Manifest, "first")
	require.Len(t, d.History, 3)
	o.Action = "uninstall"
	plan, err = p.Prepare(t.Context(), "session1", f, o, nil)
	require.NoError(t, err)
	result, err = p.Run(t.Context(), "session1", plan.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome)
	rows, err = List(a)
	require.NoError(t, err)
	require.Empty(t, rows)
}
func TestReviewRejectsChangedReleaseOwnerAndExpiry(t *testing.T) {
	a := testConfig()
	f := func(context.Context, string) (*action.Configuration, error) { return a, nil }
	p := NewPlans()
	o := Operation{Action: "install", Name: "demo", Namespace: "test", TimeoutSeconds: 30, Values: "{}"}
	first, err := p.Prepare(t.Context(), "session1", f, o, testChart())
	require.NoError(t, err)
	second, err := p.Prepare(t.Context(), "session1", f, o, testChart())
	require.NoError(t, err)
	_, err = p.Run(t.Context(), "session2", first.ID, f)
	require.Error(t, err)
	result, err := p.Run(t.Context(), "session1", first.ID, f)
	require.NoError(t, err)
	require.Equal(t, "done", result.Outcome)
	_, err = p.Run(t.Context(), "session1", second.ID, f)
	require.ErrorContains(t, err, "changed after review")
	o.Action = "uninstall"
	expired, err := p.Prepare(t.Context(), "session1", f, o, nil)
	require.NoError(t, err)
	p.mu.Lock()
	v := p.entries[expired.ID]
	v.plan.Expires = time.Now().Add(-time.Second)
	p.entries[expired.ID] = v
	p.mu.Unlock()
	_, err = p.Run(t.Context(), "session1", expired.ID, f)
	require.ErrorContains(t, err, "expired")
	rows, err := List(a)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestValuesKeepIntegerPrecisionDuringPreview(t *testing.T) {
	a := testConfig()
	p := NewPlans()
	t.Cleanup(p.Close)
	f := func(context.Context, string) (*action.Configuration, error) { return a, nil }
	o := Operation{Action: "install", Name: "precise", Namespace: "test", TimeoutSeconds: 30, Values: "greeting: 9007199254740993\n"}
	plan, err := p.Prepare(t.Context(), "session", f, o, testChart())
	require.NoError(t, err)
	require.Contains(t, plan.Manifest, `greeting: "9007199254740993"`)
}

func TestCRDInstallPreviewIsExplicitAndDoesNotChangeStorage(t *testing.T) {
	a := testConfig()
	original := a.Releases
	p := NewPlans()
	t.Cleanup(p.Close)
	ch := testChart()
	ch.Files = append(ch.Files, &common.File{Name: "crds/widgets.yaml", Data: []byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: widgets.ocular.test\nspec:\n  group: ocular.test\n  names:\n    kind: Widget\n    plural: widgets\n  scope: Namespaced\n  versions:\n    - name: v1\n      served: true\n      storage: true\n")})
	ch.Templates = append(ch.Templates, &common.File{Name: "templates/widget.yaml", Data: []byte("{{ if .Capabilities.APIVersions.Has \"ocular.test/v1/Widget\" }}\napiVersion: ocular.test/v1\nkind: Widget\nmetadata:\n  name: example\n{{ end }}")})
	f := func(context.Context, string) (*action.Configuration, error) { return a, nil }
	plan, err := p.Prepare(t.Context(), "session", f, Operation{Action: "install", Name: "demo", Namespace: "blue", TimeoutSeconds: 30, Values: "{}"}, ch)
	require.NoError(t, err)
	require.Equal(t, "client", plan.PreviewMode)
	require.Contains(t, plan.Manifest, "kind: CustomResourceDefinition")
	require.Contains(t, plan.Manifest, "kind: Widget")
	require.Same(t, original, a.Releases)
	rows, err := List(a)
	require.NoError(t, err)
	require.Empty(t, rows)
}
