package kubernetes

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// probesCRD is created and deleted by the test (cluster-scoped, no columns).
const probesCRD = "probes.ocular.dev"

var probesGVR = schema.GroupVersionResource{Group: "ocular.dev", Version: "v1", Resource: "probes"}

func probesCRDObject() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": probesCRD},
		"spec": map[string]any{
			"group": "ocular.dev", "scope": "Cluster",
			"names": map[string]any{"plural": "probes", "singular": "probe", "kind": "Probe", "shortNames": []any{"prb"}},
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true}}}},
		},
	}}
}

// The catalog on a real server: built-ins without a projection and CRDs
// come from discovery; a CRD created while the session is open appears
// (the CRD watch triggers discovery), deleted — its view ends removed.
func TestKindCatalogFollowsTheServedResources(t *testing.T) {
	p, target := kindProvider(t)
	sess, err := p.Open(context.Background(), target)
	require.NoError(t, err)
	defer sess.Close()
	s := sess.(*session)
	crds := s.dyn.Resource(crdGVR)
	_ = crds.Delete(context.Background(), probesCRD, metav1.DeleteOptions{})
	require.Eventually(t, func() bool {
		_, err := crds.Get(context.Background(), probesCRD, metav1.GetOptions{})
		return apierrors.IsNotFound(err)
	}, 60*time.Second, 200*time.Millisecond, "a probes CRD left by an earlier run is gone")

	start := time.Now()
	require.Eventually(t, func() bool { return s.Catalog().State == core.CatalogReady }, 30*time.Second, 20*time.Millisecond)
	cat := s.Catalog()
	t.Logf("discovered in %v: %d kinds, rev %d", time.Since(start), len(cat.Kinds), cat.Rev)
	byID := map[string]core.KindDescriptor{}
	for _, k := range cat.Kinds {
		byID[k.ID] = k
	}
	for _, id := range []string{"batch/jobs", "batch/cronjobs", "persistentvolumeclaims", "persistentvolumes", "storage.k8s.io/storageclasses", crdKindID} {
		assert.Contains(t, byID, id)
	}
	assert.Equal(t, "Jobs", byID["batch/jobs"].Title)
	assert.Equal(t, "batch", byID["batch/jobs"].Subgroup)
	assert.Contains(t, byID["persistentvolumeclaims"].Aliases, "pvc")
	assert.NotContains(t, byID, "events.k8s.io/events", "the events kind shows them")

	var mu sync.Mutex
	var told []uint64
	s.OnKindsChanged(func(rev uint64) { mu.Lock(); told = append(told, rev); mu.Unlock() })
	_, err = crds.Create(context.Background(), probesCRDObject(), metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = crds.Delete(context.Background(), probesCRD, metav1.DeleteOptions{}) })
	require.Eventually(t, func() bool {
		for _, k := range s.Catalog().Kinds {
			if k.ID == "ocular.dev/probes" {
				return true
			}
		}
		return false
	}, 30*time.Second, 50*time.Millisecond, "a created CRD appears without F5")

	probes := s.dyn.Resource(probesGVR)
	_, err = probes.Create(context.Background(), &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ocular.dev/v1", "kind": "Probe", "metadata": map[string]any{"name": "one"}}}, metav1.CreateOptions{})
	require.NoError(t, err)
	sink := &recordingStatusSink{}
	stop, err := s.Watch(provider.Query{Kind: "ocular.dev/probes", Scope: core.ScopeSel{Mode: core.ScopeAll}}, sink)
	require.NoError(t, err)
	defer stop()
	require.Eventually(t, func() bool { return sink.last().State == provider.StatusReady }, 30*time.Second, 20*time.Millisecond)

	require.NoError(t, crds.Delete(context.Background(), probesCRD, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return sink.last().Class == provider.ClassRemoved }, 60*time.Second, 50*time.Millisecond, "deleted CRD: its view ends removed")
	assert.True(t, s.KindRemoved("ocular.dev/probes"))
	mu.Lock()
	defer mu.Unlock()
	assert.GreaterOrEqual(t, len(told), 2, "revisions told: added, removed")
}
