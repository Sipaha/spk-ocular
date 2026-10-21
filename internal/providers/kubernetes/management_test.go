package kubernetes

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestReviewManagementTracksPreparedObjectWithoutInventory(t *testing.T) {
	cm := configMap("cfg", "7", map[string]any{"key": "old"}, map[string]any{"annotations": map[string]any{"argocd.argoproj.io/tracking-id": "old:/ConfigMap:ns/cfg", "private": "PRIVATE-ANNOTATION"}})
	s, w := editSession(t, cm)
	s.now = func() time.Time { return time.Unix(100, 0) }
	doc, base := source(t, s, cmRef("cfg"))
	// The review reads current metadata, not tracking copied from the editor's
	// older text. A disjoint metadata update must not relabel the review's source.
	cm.SetResourceVersion("8")
	cm.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "current:/ConfigMap:ns/cfg", "private": "PRIVATE-ANNOTATION"})
	_, err := s.dyn.Resource(cmGVR).Namespace("ns").Update(t.Context(), cm, metav1.UpdateOptions{})
	require.NoError(t, err)
	client := w.dyn.(*dynamicfake.FakeDynamicClient)
	client.ClearActions()
	plan, grant, err := s.PrepareEdit(t.Context(), request(doc, base, strings.Replace(doc.Text, "key: old", "key: changed", 1)))
	require.NoError(t, err)
	require.NotNil(t, grant)
	require.NotNil(t, plan.Management)
	require.Equal(t, plan.Where.Ref, plan.Management.Ref)
	require.Equal(t, "uid-cfg", plan.Management.Ref.UID)
	require.Equal(t, "8", plan.Management.Version)
	require.Equal(t, "current", plan.Management.ArgoTracking.Application)
	require.EqualValues(t, 100000, plan.Management.CapturedAt)
	encoded, err := json.Marshal(plan.Management)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE")
	require.NotContains(t, string(encoded), "changed")
	require.Empty(t, w.writes(false), "review does not write")
	// The fake client used by editSession is the only transport. No new source
	// inventory or credential request is needed to attach the tracking hints.
	for _, a := range client.Actions() {
		require.NotEqual(t, "list", a.GetVerb())
		require.NotContains(t, a.GetResource().Group, "toolkit.fluxcd.io")
		require.NotEqual(t, "argoproj.io", a.GetResource().Group)
		require.NotEqual(t, "secrets", a.GetResource().Resource)
	}
}

func TestReviewManagementScaleOnlyAndNoControllerReads(t *testing.T) {
	u := workload("Deployment", "web", "uid-web", "9", map[string]any{"replicas": int64(2)})
	u.SetLabels(map[string]string{"kustomize.toolkit.fluxcd.io/name": "delivery", "kustomize.toolkit.fluxcd.io/namespace": "gitops"})
	s, client := actionSession(t, u)
	next := 3
	plan := prepare(t, s, deployWebRef, "scale", core.ActionParams{Count: &next})
	require.NotNil(t, plan.Management)
	require.Equal(t, deployWebRef, plan.Management.Ref)
	require.Equal(t, "9", plan.Management.Version)
	require.Nil(t, plan.Management.ArgoTracking)
	require.Equal(t, []core.FluxManagerHint{{Kind: "Kustomization", Name: "delivery", Namespace: "gitops"}}, plan.Management.FluxTracking.Candidates)
	require.Empty(t, writes(client))
	for _, a := range client.Actions() {
		require.NotContains(t, a.GetResource().Group, "toolkit.fluxcd.io")
		require.NotEqual(t, "argoproj.io", a.GetResource().Group)
		require.NotEqual(t, "secrets", a.GetResource().Resource)
	}
	restart := prepare(t, s, deployWebRef, "restart", core.ActionParams{})
	require.Nil(t, restart.Management, "this stage does not add context to unrelated actions")
}

func TestReviewManagementRejectsCopiedTrackingAndBoundsOwnerHints(t *testing.T) {
	u := configMap("cfg", "7", map[string]any{"key": "PRIVATE-VALUE"}, nil)
	s, _ := editSession(t, u)
	require.Nil(t, s.reviewManagement(configMapsKind, u), "no hint does not imply unmanaged")
	u.SetAnnotations(map[string]string{"argocd.argoproj.io/tracking-id": "app:/ConfigMap:other/cfg"})
	require.Nil(t, s.reviewManagement(configMapsKind, u))
	u.SetAnnotations(nil)
	owners := []metav1.OwnerReference{}
	for i := 0; i < 40; i++ {
		owners = append(owners, metav1.OwnerReference{APIVersion: "kustomize.toolkit.fluxcd.io/v1", Kind: "Kustomization", Name: fmt.Sprintf("owner-%d", i), UID: "uid"})
	}
	u.SetOwnerReferences(owners)
	observed := s.reviewManagement(configMapsKind, u)
	require.True(t, observed.Truncated)
	require.Len(t, observed.FluxTracking.Candidates, maxReviewManagerHints)
	observed.FluxTracking.Candidates[0].Name = "changed"
	require.Equal(t, "owner-0", s.reviewManagement(configMapsKind, u).FluxTracking.Candidates[0].Name)
	owners[0].UID = ""
	u.SetOwnerReferences(owners[:1])
	require.Nil(t, s.reviewManagement(configMapsKind, u), "an empty owner UID cannot produce a pinned manager")
	owners[0].UID = "oversized"
	u.SetOwnerReferences(owners[:1])
	u.Object["metadata"].(map[string]any)["ownerReferences"].([]any)[0].(map[string]any)["uid"] = strings.Repeat("u", 257)
	observed = s.reviewManagement(configMapsKind, u)
	require.True(t, observed.Truncated)
	require.Empty(t, observed.FluxTracking.Candidates)
	u.SetUID("")
	require.Nil(t, s.reviewManagement(configMapsKind, u))
}
