package kubernetes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func widget(name string, finalizers ...string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "ocular.dev/v1", "kind": "Widget",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": "uid-" + name, "resourceVersion": "5", "creationTimestamp": "2026-09-30T10:00:00Z"}}}
	if len(finalizers) > 0 {
		u.SetFinalizers(finalizers)
	}
	return u
}

func widgetSession(t *testing.T, objs ...*unstructured.Unstructured) (*session, *scriptedAPI) {
	t.Helper()
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {widgets}}, "ocular.dev"))
	var ro []runtime.Object
	for _, o := range objs {
		ro = append(ro, o)
	}
	s := newSession("ctx", "h", catalogClient(ro...), false)
	t.Cleanup(s.Close)
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	return s, api
}

func deletes(s *session) int {
	n := 0
	for _, a := range s.dyn.(interface{ Actions() []k8stesting.Action }).Actions() {
		if a.GetVerb() == "delete" {
			n++
		}
	}
	return n
}

// A discovered kind's delete: the finalizers are said, and the plan holds
// them; a plan reviewed through another version of the resource is never
// carried over.
func TestDiscoveredDeleteHoldsItsRouteAndFinalizers(t *testing.T) {
	s, api := widgetSession(t, widget("alpha", "ocular.dev/hold"))
	ref := core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha"}
	plan, err := s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	var texts []string
	for _, e := range plan.Effects {
		texts = append(texts, e.Text)
	}
	assert.Contains(t, strings.Join(texts, " "), "Deletion waits for its finalizers: ocular.dev/hold.")

	// finalizers changed after the review: a conflict, nothing deleted
	w := s.dyn.Resource(widgetsGVR).Namespace("ns")
	u, _ := w.Get(context.Background(), "alpha", metav1.GetOptions{})
	u.SetFinalizers([]string{"ocular.dev/hold", "ocular.dev/more"})
	_, err = w.Update(context.Background(), u, metav1.UpdateOptions{})
	require.NoError(t, err)
	ref.UID = "uid-alpha"
	_, err = s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "delete", Expect: plan.Expect})
	var perr *provider.Error
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassConflict, perr.Class)
	assert.Equal(t, "error.changed", saidKey(t, err))
	assert.Zero(t, deletes(s))

	// the resource moved to another version: the reviewed plan is refused
	plan, err = s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	v2 := widgets
	v2.version = "v2"
	api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {v2, widgets}}, "ocular.dev"))
	s.RefreshKinds()
	s.cat.wait()
	require.Equal(t, "v2", s.kind("ocular.dev/widgets").gvr.Version)
	_, err = s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "delete", Expect: plan.Expect})
	require.True(t, errors.As(err, &perr))
	assert.Equal(t, provider.ClassConflict, perr.Class)
	assert.Contains(t, perr.Message, "API resource")
	assert.Equal(t, "error.routeChanged", saidKey(t, err))
	assert.Zero(t, deletes(s))
}

func TestDiscoveredDeleteRuns(t *testing.T) {
	s, _ := widgetSession(t, widget("alpha"))
	ref := core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha", UID: "uid-alpha"}
	plan, err := s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	res, err := s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "delete", Expect: plan.Expect})
	require.NoError(t, err)
	assert.Equal(t, "widget alpha: deletion requested", res.Message.Text)
	assertDone(t, res, "done.delete", map[string]string{"kind": "widget", "name": "alpha"})
	assert.Equal(t, 1, deletes(s))
}

// An owner of a discovered kind opens (it is in the catalog).
func TestOwnersOfDiscoveredKindsOpen(t *testing.T) {
	p := pod("ns", "p", "uid-p")
	delete(p.Object["metadata"].(map[string]any), "annotations")
	p.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "ocular.dev/v1", Kind: "Widget", Name: "alpha", UID: "uid-alpha"}})
	s, _ := widgetSession(t, p)
	r, err := s.Get(context.Background(), core.Ref{Kind: "pods", Scope: "ns", Name: "p"})
	require.NoError(t, err)
	require.NotEmpty(t, r.Relations)
	assert.Equal(t, "ocular.dev/widgets", r.Relations[0].Ref.Kind)
	assert.False(t, r.Relations[0].Inert)
}

// A run keeps the route it checked: the resource moving to another
// version between a refused write and its retry never mixes versions
// (reads and writes all through the reviewed one) (Codex P2-1 of 77b7b11).
func TestARunKeepsItsRouteThroughRetries(t *testing.T) {
	s, api := widgetSession(t, widget("alpha"))
	ref := core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha", UID: "uid-alpha"}
	plan, err := s.PrepareAction(context.Background(), ref, "delete", core.ActionParams{})
	require.NoError(t, err)
	fake := s.dyn.(*dynamicfake.FakeDynamicClient)
	var moved bool
	s.beforeWrite = func(string, *unstructured.Unstructured) {
		if moved {
			return
		}
		moved = true
		v2 := widgets
		v2.version = "v2"
		api.set("/apis", apisDoc(map[string][]v2ver{"ocular.dev": {v2, widgets}}, "ocular.dev"))
		s.RefreshKinds()
		s.cat.wait()
		require.Equal(t, "v2", s.kind("ocular.dev/widgets").gvr.Version)
	}
	var refused bool
	fake.PrependReactor("delete", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
		if refused {
			return false, nil, nil
		}
		refused = true
		// Someone else touched it: a failed precondition at a moved version.
		u := widget("alpha")
		u.SetResourceVersion("6")
		require.NoError(t, fake.Tracker().Update(widgetsGVR, u, "ns", metav1.UpdateOptions{}))
		return true, nil, apierrors.NewConflict(widgetsGVR.GroupResource(), "alpha", errors.New("precondition failed"))
	})
	_, err = s.RunAction(context.Background(), provider.ActionRun{Ref: ref, Action: "delete", Expect: plan.Expect})
	var versions []string
	for _, a := range fake.Actions() {
		if a.GetResource().Resource == "widgets" {
			versions = append(versions, a.GetVerb()+" "+a.GetResource().Version)
		}
	}
	assert.NotContains(t, strings.Join(versions, ","), "v2", "one route for the run: %v", versions)
	if err == nil {
		assert.Equal(t, 2, deletes(s), "retried through the reviewed route")
	}
}

// Details of a kind read in the plain format say the health its list says
// (conditions), not the catalog placeholder's unknown (Codex P2-2 of
// 77b7b11).
func TestPlainDetailsReadTheConditions(t *testing.T) {
	w := widget("alpha")
	w.SetGeneration(2)
	w.Object["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False", "reason": "Broken", "observedGeneration": int64(2)}}}
	s, _ := widgetSession(t, w)
	r, err := s.Get(context.Background(), core.Ref{Kind: "ocular.dev/widgets", Scope: "ns", Name: "alpha"})
	require.NoError(t, err)
	assert.Equal(t, core.HealthError, r.Health.State)
	assert.Equal(t, "Broken", r.Health.Reason)
}
