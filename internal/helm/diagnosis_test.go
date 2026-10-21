package helm

import (
	releaseapi "helm.sh/helm/v4/pkg/release"
	"helm.sh/helm/v4/pkg/storage/driver"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	common "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
)

func TestHelmDiagnosisRecordedHistoryAndHooks(t *testing.T) {
	a := testConfig()
	for i, status := range []common.Status{common.StatusSuperseded, common.StatusFailed, common.StatusPendingRollback, common.StatusDeployed} {
		r := &release.Release{Name: "demo", Namespace: "workloads", Version: i + 1, Chart: testChart(), Info: &release.Info{Status: status}, Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n"}
		if i == 3 {
			r.Info.RollbackRevision = 1
		}
		r.Hooks = []*release.Hook{{Name: "check", Kind: "Job", Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: check\n", LastRun: release.HookExecution{Phase: release.HookPhaseFailed, StartedAt: time.Now()}}}
		require.NoError(t, a.Releases.Create(r))
	}
	d, err := Diagnose(t.Context(), a, "demo", 2)
	require.NoError(t, err)
	require.Equal(t, "failed", d.Selected.Status)
	require.Equal(t, 1, d.CompareFrom)
	require.Equal(t, 2, d.CompareTo)
	require.Equal(t, "workloads", d.Hooks[0].Namespace)
	require.Equal(t, "supported", d.Hooks[0].Coverage)
	require.Equal(t, "Failed", d.Hooks[0].Phase)
	pending, err := Diagnose(t.Context(), a, "demo", 3)
	require.NoError(t, err)
	require.Equal(t, "pending-rollback", pending.Selected.Status)
	latest, err := Diagnose(t.Context(), a, "demo", 4)
	require.NoError(t, err)
	require.Equal(t, 1, latest.Selected.RollbackRevision)
	first, err := Diagnose(t.Context(), a, "demo", 1)
	require.NoError(t, err)
	require.Zero(t, first.CompareFrom)
	c, err := Compare(t.Context(), a, "demo", 1, 4)
	require.NoError(t, err)
	require.Equal(t, c.Left, c.Right)
	raw, err := stored(a, "demo", 2)
	require.NoError(t, err)
	raw.Hooks[0].Manifest += "\n# replacement"
	require.NoError(t, a.Releases.Update(raw))
	require.ErrorContains(t, d.Verify(a), "changed")
}
func TestHelmManifestComparisonSecretProjectionAndRejections(t *testing.T) {
	text := `apiVersion: v1
kind: List
items:
- apiVersion: v1
  kind: Secret
  metadata:
    name: password
    annotations:
      leak: sentinel
  data:
    pass: sentinel
  stringData:
    pass: sentinel
- apiVersion: v1
  kind: List
  items:
  - apiVersion: v1
    kind: Secret
    metadata:
      name: nested
    custom: sentinel
- apiVersion: example.test/v1
  kind: Settings
  spec:
    password: opaque-retained
`
	normalized, err := NormalizeManifest(text)
	require.NoError(t, err)
	require.NotContains(t, normalized, "sentinel")
	require.NotContains(t, normalized, "annotations")
	require.Contains(t, normalized, "nested")
	require.Contains(t, normalized, "opaque-retained")
	typed, err := NormalizeManifest("apiVersion: v1\nkind: SecretList\nmetadata:\n  annotations:\n    a: sentinel\nitems:\n- metadata:\n    name: typed\n  data:\n    p: sentinel\n")
	require.NoError(t, err)
	require.NotContains(t, typed, "sentinel")
	require.Contains(t, typed, "typed")
	again, err := NormalizeManifest(normalized)
	require.NoError(t, err)
	require.Equal(t, normalized, again)
	for _, input := range []string{"a: 1\na: 2\n", "a: &a secret\nb: *a", "a: [", "a:\n" + strings.Repeat("  a:\n", 70), strings.Repeat("x", (4<<20)+1), strings.Repeat("---\nkind: Pod\n", 257), "? [a,b]\n: x"} {
		_, err := NormalizeManifest(input)
		require.Error(t, err, input[:min(len(input), 100)])
	}
	_, err = NormalizeManifest("data: " + strings.Repeat("x", (1<<20)+1))
	require.Error(t, err)
}

func TestHelmDiagnosisBudgetsAndUnsupportedHooks(t *testing.T) {
	a := testConfig()
	r := &release.Release{Name: "demo", Namespace: "workloads", Version: 1, Chart: testChart(), Info: &release.Info{Status: common.StatusFailed}, Hooks: []*release.Hook{{Name: "cross", Manifest: "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: cross\n  namespace: other\n"}, {Name: "opaque", Manifest: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: opaque\ndata:\n  value: PRIVATE\n"}, {Name: "large", Manifest: strings.Repeat("x", (256<<10)+1)}}}
	for i := 0; i < 33; i++ {
		r.Hooks = append(r.Hooks, &release.Hook{Name: "more", Manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: more\n"})
	}
	require.NoError(t, a.Releases.Create(r))
	d, err := Diagnose(t.Context(), a, "demo", 1)
	require.NoError(t, err)
	require.True(t, d.Truncated)
	require.Len(t, d.Hooks, 32)
	require.Equal(t, "unsupported", d.Hooks[0].Coverage)
	require.Equal(t, "unsupported", d.Hooks[1].Coverage)
	require.Equal(t, "partial", d.Hooks[2].Coverage)
	depth := ""
	for i := 0; i < 66; i++ {
		depth += strings.Repeat("  ", i) + "a:\n"
	}
	_, err = NormalizeManifest(depth)
	require.Error(t, err)
	_, err = NormalizeManifest("items:\n" + strings.Repeat("- v\n", 100001))
	require.Error(t, err)
}

type changingRevisionDriver struct {
	driver.Driver
	gets int
}

func (d *changingRevisionDriver) Get(key string) (releaseapi.Releaser, error) {
	raw, err := d.Driver.Get(key)
	if err != nil {
		return raw, err
	}
	d.gets++
	if d.gets == 3 {
		r, err := asRelease(raw)
		if err != nil {
			return nil, err
		}
		replacement := *r
		replacement.Manifest += "\n# replaced"
		return &replacement, nil
	}
	return raw, nil
}
func TestHelmComparisonRejectsStoredReplacementWithoutPartialStrings(t *testing.T) {
	a := testConfig()
	for i := 1; i <= 2; i++ {
		require.NoError(t, a.Releases.Create(&release.Release{Name: "demo", Namespace: "workloads", Version: i, Chart: testChart(), Info: &release.Info{Status: common.StatusDeployed}, Manifest: "apiVersion: v1\nkind: Pod\nmetadata:\n  name: demo\n"}))
	}
	a.Releases.Driver = &changingRevisionDriver{Driver: a.Releases.Driver}
	out, err := Compare(t.Context(), a, "demo", 1, 2)
	require.ErrorContains(t, err, "changed")
	require.Empty(t, out.Left)
	require.Empty(t, out.Right)
}
