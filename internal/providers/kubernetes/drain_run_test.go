package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type evictCall struct{ ns, name, uid, rv string }

// drainRec records a drain's writes: the cordon (a merge patch applied to
// the fake client unless cordonErr answers) and each eviction request
// (answered by answer; nil: the pod is marked deleted).
type drainRec struct {
	dyn       dynamic.Interface
	mu        sync.Mutex
	patches   int
	evictions []evictCall
	cordonErr func(n int) error
	answer    func(ctx context.Context, n int, c evictCall) error
}

func (w *drainRec) patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, _ string) error {
	w.mu.Lock()
	w.patches++
	n, f := w.patches, w.cordonErr
	w.mu.Unlock()
	if f != nil {
		if err := f(n); err != nil {
			return err
		}
	}
	_, err := w.dyn.Resource(gvr).Namespace(ns).Patch(ctx, name, pt, data, metav1.PatchOptions{})
	return err
}

func (w *drainRec) delete(context.Context, schema.GroupVersionResource, string, string, metav1.DeleteOptions) error {
	return errors.New("a drain never deletes")
}

func (w *drainRec) editPatch(context.Context, schema.GroupVersionResource, string, string, []byte, bool) ([]byte, error) {
	return nil, errors.New("unexpected edit")
}

func (w *drainRec) evict(ctx context.Context, ns, name, uid, rv string) error {
	c := evictCall{ns, name, uid, rv}
	w.mu.Lock()
	w.evictions = append(w.evictions, c)
	n, f := len(w.evictions), w.answer
	w.mu.Unlock()
	if f != nil {
		return f(ctx, n, c)
	}
	return nil
}

func (w *drainRec) calls() []evictCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]evictCall{}, w.evictions...)
}

func runSession(t *testing.T, objs ...kruntime.Object) (*session, *dynamicfake.FakeDynamicClient, *drainRec) {
	t.Helper()
	s, c := drainSession(t, objs...)
	w := &drainRec{dyn: c}
	s.writer = w
	return s, c, w
}

// drainPlan is the plan the UI would run.
func drainPlan(t *testing.T, s *session) core.ActionPlan {
	t.Helper()
	plan := prepare(t, s, nodeRef("w1", ""), "drain", core.ActionParams{})
	require.Nil(t, plan.Unavailable)
	return plan
}

func runDrainPlan(s *session, plan core.ActionPlan) (core.ActionResult, error) {
	return s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: "drain", Expect: plan.Expect})
}

type partSeen struct {
	title   string
	outcome core.ActionOutcome
	why     string
}

func partsOf(res core.ActionResult) []partSeen {
	var out []partSeen
	for _, p := range res.Parts {
		why := ""
		if p.Why != nil {
			why = p.Why.Key
		}
		out = append(out, partSeen{p.Title, p.Outcome, why})
	}
	return out
}

// twoPods: an open node w1, two pods to evict (a/api-1, a/web-1) and one
// without a controller.
func twoPods() []kruntime.Object {
	return []kruntime.Object{
		node("w1", "uid-w1", "5", false),
		nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "web-rs", "uid-rs")),
		nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api")),
		nodePod("a", "bare", "uid-bare", "w1"),
	}
}

func setPod(t *testing.T, c *dynamicfake.FakeDynamicClient, u *unstructured.Unstructured) {
	t.Helper()
	require.NoError(t, c.Tracker().Update(podGVR, u, u.GetNamespace()))
}

func evictionStatus(code int32, reason metav1.StatusReason, causes ...metav1.StatusCause) error {
	st := metav1.Status{Status: metav1.StatusFailure, Code: code, Reason: reason, Message: string(reason)}
	if len(causes) > 0 {
		st.Details = &metav1.StatusDetails{Causes: causes}
	}
	return &apierrors.StatusError{ErrStatus: st}
}

func TestADrainCordonsThenEvictsEachPodOnce(t *testing.T) {
	s, c, w := runSession(t, twoPods()...)
	plan := drainPlan(t, s)
	res, err := runDrainPlan(s, plan)
	require.NoError(t, err)
	assert.Equal(t, []partSeen{
		{"w1", core.OutcomeDone, ""},
		{"a/api-1", core.OutcomeDone, ""},
		{"a/web-1", core.OutcomeDone, ""},
		{"a/bare", core.OutcomeSkipped, "kubernetes.drain.why.bare"},
	}, partsOf(res))
	assert.Equal(t, core.OutcomeSkipped, res.Outcome, "a pod stays: not everything")
	assert.Equal(t, 1, w.patches)
	assert.Equal(t, []evictCall{{"a", "api-1", "uid-api-1", "1"}, {"a", "web-1", "uid-web-1", "1"}}, w.calls(), "one request per pod, with the UID and version of the run's list")
	n, err := c.Resource(nodeGVR).Get(context.Background(), "w1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.True(t, boolAt(n.Object, "spec", "unschedulable"))
}

func TestADrainOfACordonedNodeWritesNoCordon(t *testing.T) {
	objs := twoPods()[:3]
	objs[0] = node("w1", "uid-w1", "5", true)
	s, _, w := runSession(t, objs...)
	res, err := runDrainPlan(s, drainPlan(t, s))
	require.NoError(t, err)
	assert.Equal(t, 0, w.patches)
	assert.Equal(t, core.OutcomeDone, res.Outcome)
	assert.Len(t, res.Parts, 2)
}

// Each eviction answer and what it proves; every attempt is one request.
func TestEvictionAnswersAndWhatTheyProve(t *testing.T) {
	pdbCause := metav1.StatusCause{Type: disruptionBudgetCause, Message: "needs 2 healthy pods"}
	cases := []struct {
		name  string
		setup func(t *testing.T, c *dynamicfake.FakeDynamicClient)
		// answer to the first eviction of a/api-1 (the others succeed)
		first   func(c *dynamicfake.FakeDynamicClient) error
		outcome core.ActionOutcome
		why     string
		calls   []evictCall // of a/api-1
	}{
		{name: "429 of a PodDisruptionBudget", first: func(*dynamicfake.FakeDynamicClient) error {
			return evictionStatus(429, metav1.StatusReasonTooManyRequests, pdbCause)
		}, outcome: core.OutcomeRefused, why: "drain.why.pdb", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "429 throttled, with Retry-After", first: func(*dynamicfake.FakeDynamicClient) error {
			return apierrors.NewTooManyRequests("slow down", 5)
		}, outcome: core.OutcomeRefused, why: "drain.why.throttled", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "409 at the same version", first: func(*dynamicfake.FakeDynamicClient) error {
			return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "api-1", errors.New("storage"))
		}, outcome: core.OutcomeRefused, why: "drain.why.refused", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "409 at a new version, the same pod: tried again with it", first: func(c *dynamicfake.FakeDynamicClient) error {
			u := nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api"))
			u.SetResourceVersion("7")
			_ = c.Tracker().Update(podGVR, u, "a")
			return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "api-1", errors.New("the object has been modified"))
		}, outcome: core.OutcomeDone, calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}, {"a", "api-1", "uid-api-1", "7"}}},
		{name: "409, its controller removed meanwhile: left, not evicted", first: func(c *dynamicfake.FakeDynamicClient) error {
			u := nodePod("a", "api-1", "uid-api-1", "w1")
			u.SetResourceVersion("7")
			_ = c.Tracker().Update(podGVR, u, "a")
			return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "api-1", errors.New("the object has been modified"))
		}, outcome: core.OutcomeRefused, why: "drain.why.changed", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "404 of a live pod (no eviction route): refused, no delete", first: func(*dynamicfake.FakeDynamicClient) error {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "pods/eviction"}, "api-1")
		}, outcome: core.OutcomeRefused, why: "drain.why.refused", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "404 of a live pod at a new version: still refused", first: func(c *dynamicfake.FakeDynamicClient) error {
			u := nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api"))
			u.SetResourceVersion("8")
			_ = c.Tracker().Update(podGVR, u, "a")
			return apierrors.NewNotFound(schema.GroupResource{Resource: "pods/eviction"}, "api-1")
		}, outcome: core.OutcomeRefused, why: "drain.why.refused", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "404, the pod gone", first: func(c *dynamicfake.FakeDynamicClient) error {
			_ = c.Tracker().Delete(podGVR, "a", "api-1")
			return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, "api-1")
		}, outcome: core.OutcomeSkipped, why: "drain.why.gone", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "409, the pod replaced: the new one is not evicted", first: func(c *dynamicfake.FakeDynamicClient) error {
			_ = c.Tracker().Delete(podGVR, "a", "api-1")
			_ = c.Tracker().Create(podGVR, nodePod("a", "api-1", "uid-new", "w1", ownedBy("StatefulSet", "api", "uid-api")), "a")
			return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "api-1", errors.New("uid"))
		}, outcome: core.OutcomeSkipped, why: "drain.why.replaced", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "500: unknown, not repeated", first: func(*dynamicfake.FakeDynamicClient) error {
			return apierrors.NewInternalError(errors.New("more than one PodDisruptionBudget"))
		}, outcome: core.OutcomeUnknown, why: "drain.why.unknown", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "no answer: unknown", first: func(*dynamicfake.FakeDynamicClient) error {
			return errors.New("connection reset")
		}, outcome: core.OutcomeUnknown, why: "drain.why.unknown", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
		{name: "403", first: func(*dynamicfake.FakeDynamicClient) error {
			return apierrors.NewForbidden(schema.GroupResource{Resource: "pods/eviction"}, "api-1", errors.New("rbac"))
		}, outcome: core.OutcomeRefused, why: "drain.why.refused", calls: []evictCall{{"a", "api-1", "uid-api-1", "1"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, c, w := runSession(t, twoPods()...)
			plan := drainPlan(t, s)
			seen := 0
			w.answer = func(_ context.Context, _ int, call evictCall) error {
				if call.name == "api-1" {
					seen++
					if seen == 1 {
						return tc.first(c)
					}
				}
				return nil
			}
			res, err := runDrainPlan(s, plan)
			require.NoError(t, err)
			api := res.Parts[1]
			assert.Equal(t, "a/api-1", api.Title)
			assert.Equal(t, tc.outcome, api.Outcome)
			if tc.why != "" {
				require.NotNil(t, api.Why)
				assert.Equal(t, "kubernetes."+tc.why, api.Why.Key)
			}
			var mine []evictCall
			for _, call := range w.calls() {
				if call.name == "api-1" {
					mine = append(mine, call)
				}
			}
			assert.Equal(t, tc.calls, mine)
			assert.Equal(t, core.OutcomeDone, res.Parts[2].Outcome, "a refusal or an unknown outcome does not stop the next eviction")
		})
	}
}

func TestEvictionRetriesAreBoundedInAll(t *testing.T) {
	s, c, w := runSession(t, twoPods()...)
	plan := drainPlan(t, s)
	rv := 10
	w.answer = func(_ context.Context, _ int, call evictCall) error {
		if call.name != "api-1" {
			return nil
		}
		rv++
		u := nodePod("a", "api-1", "uid-api-1", "w1", ownedBy("StatefulSet", "api", "uid-api"))
		u.SetResourceVersion(fmt.Sprint(rv))
		_ = c.Tracker().Update(podGVR, u, "a")
		return apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "api-1", errors.New("modified"))
	}
	res, err := runDrainPlan(s, plan)
	require.NoError(t, err)
	n := 0
	for _, call := range w.calls() {
		if call.name == "api-1" {
			n++
		}
	}
	assert.Equal(t, maxVersionRetries, n, "attempts in all, the first included")
	assert.Equal(t, "kubernetes.drain.why.keepsChanging", res.Parts[1].Why.Key)
}

// The run's outcome is its worst part's: a later refusal or success never
// hides an earlier unknown.
func TestADrainsOutcomeIsItsWorstParts(t *testing.T) {
	objs := []kruntime.Object{node("w1", "uid-w1", "5", true)}
	for _, n := range []string{"p1", "p2", "p3"} {
		objs = append(objs, nodePod("a", n, "uid-"+n, "w1", ownedBy("ReplicaSet", "rs", "uid-rs")))
	}
	s, _, w := runSession(t, objs...)
	plan := drainPlan(t, s)
	w.answer = func(_ context.Context, n int, _ evictCall) error {
		switch n {
		case 1:
			return errors.New("connection reset")
		case 2:
			return evictionStatus(429, metav1.StatusReasonTooManyRequests, metav1.StatusCause{Type: disruptionBudgetCause})
		}
		return nil
	}
	res, err := runDrainPlan(s, plan)
	require.NoError(t, err)
	assert.Equal(t, []core.ActionOutcome{core.OutcomeUnknown, core.OutcomeRefused, core.OutcomeDone}, []core.ActionOutcome{res.Parts[0].Outcome, res.Parts[1].Outcome, res.Parts[2].Outcome})
	assert.Equal(t, core.OutcomeUnknown, res.Outcome)
}

func TestAFailedCordonEvictsNothing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome core.ActionOutcome
	}{
		{"refused", apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "w1", errors.New("rbac")), core.OutcomeRefused},
		{"unknown", apierrors.NewInternalError(errors.New("boom")), core.OutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _, w := runSession(t, twoPods()...)
			plan := drainPlan(t, s)
			w.cordonErr = func(int) error { return tc.err }
			res, err := runDrainPlan(s, plan)
			require.NoError(t, err, "a write was sent: a result, not an error")
			assert.Empty(t, w.calls())
			assert.Equal(t, []partSeen{
				{"w1", tc.outcome, map[core.ActionOutcome]string{core.OutcomeRefused: "kubernetes.drain.why.refused", core.OutcomeUnknown: "kubernetes.drain.why.unknown"}[tc.outcome]},
				{"a/api-1", core.OutcomeSkipped, "kubernetes.drain.why.notCordoned"},
				{"a/web-1", core.OutcomeSkipped, "kubernetes.drain.why.notCordoned"},
				{"a/bare", core.OutcomeSkipped, "kubernetes.drain.why.bare"},
			}, partsOf(res))
			assert.Equal(t, tc.outcome, res.Outcome)
		})
	}
}

// A cordon refused at a moved version is written again only after the
// whole plan (node and pods) is checked again.
func TestACordonRetryChecksTheWholePlanAgain(t *testing.T) {
	t.Run("status churn: retried", func(t *testing.T) {
		s, c, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		w.cordonErr = func(n int) error {
			if n == 1 {
				_ = c.Tracker().Update(nodeGVR, node("w1", "uid-w1", "6", false), "")
				return apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, "w1", errors.New("modified"))
			}
			return nil
		}
		res, err := runDrainPlan(s, plan)
		require.NoError(t, err)
		assert.Equal(t, 2, w.patches)
		assert.Equal(t, core.OutcomeDone, res.Parts[0].Outcome)
	})
	t.Run("a pod changed meanwhile: a conflict, nothing written", func(t *testing.T) {
		s, c, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		w.cordonErr = func(int) error {
			_ = c.Tracker().Update(nodeGVR, node("w1", "uid-w1", "6", false), "")
			_ = c.Tracker().Update(podGVR, nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "other-rs", "uid-rs2")), "a")
			return apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, "w1", errors.New("modified"))
		}
		_, err := runDrainPlan(s, plan)
		var pe *provider.Error
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, provider.ClassConflict, pe.Class)
		assert.Equal(t, 1, w.patches)
		assert.Empty(t, w.calls())
	})
}

func TestADrainNotMatchingItsPlanWritesNothing(t *testing.T) {
	t.Run("another controller behind the same UIDs", func(t *testing.T) {
		s, c, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		setPod(t, c, nodePod("a", "web-1", "uid-web-1", "w1", ownedBy("ReplicaSet", "other-rs", "uid-rs2")))
		_, err := runDrainPlan(s, plan)
		var pe *provider.Error
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, provider.ClassConflict, pe.Class)
		assert.Equal(t, 0, w.patches)
		assert.Empty(t, w.calls())
	})
	t.Run("the list cut short", func(t *testing.T) {
		s, c, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		for i := range maxDrainPods {
			require.NoError(t, c.Tracker().Create(podGVR, nodePod("b", fmt.Sprintf("x-%d", i), fmt.Sprintf("uid-x-%d", i), "w1", ownedBy("ReplicaSet", "rs", "uid-rs")), "b"))
		}
		_, err := runDrainPlan(s, plan)
		var pe *provider.Error
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, provider.ClassConflict, pe.Class)
		assert.Equal(t, 0, w.patches)
		assert.Empty(t, w.calls())
	})
}

func TestADrainsTimeAndCancellation(t *testing.T) {
	t.Run("out of time before the first write: an error, nothing written", func(t *testing.T) {
		s, _, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		old := drainRunTimeout
		drainRunTimeout = time.Nanosecond
		t.Cleanup(func() { drainRunTimeout = old })
		_, err := runDrainPlan(s, plan)
		require.Error(t, err)
		assert.Equal(t, 0, w.patches)
		assert.Empty(t, w.calls())
	})
	t.Run("out of time during an eviction: it is unknown, the rest not started", func(t *testing.T) {
		s, _, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		old := drainRunTimeout
		drainRunTimeout = 300 * time.Millisecond
		t.Cleanup(func() { drainRunTimeout = old })
		w.answer = func(ctx context.Context, _ int, _ evictCall) error {
			<-ctx.Done() // the first eviction hangs
			return ctx.Err()
		}
		res, err := runDrainPlan(s, plan)
		require.NoError(t, err)
		assert.Equal(t, []partSeen{
			{"w1", core.OutcomeDone, ""},
			{"a/api-1", core.OutcomeUnknown, "kubernetes.drain.why.unknown"},
			{"a/web-1", core.OutcomeSkipped, "kubernetes.drain.why.timeUp"},
			{"a/bare", core.OutcomeSkipped, "kubernetes.drain.why.bare"},
		}, partsOf(res))
		assert.Len(t, w.calls(), 1)
		assert.Equal(t, core.OutcomeUnknown, res.Outcome)
	})
	t.Run("a write over its own deadline: unknown, the run goes on", func(t *testing.T) {
		s, _, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		old := drainWriteTimeout
		drainWriteTimeout = 50 * time.Millisecond
		t.Cleanup(func() { drainWriteTimeout = old })
		w.answer = func(ctx context.Context, n int, _ evictCall) error {
			if n == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		res, err := runDrainPlan(s, plan)
		require.NoError(t, err)
		assert.Equal(t, core.OutcomeUnknown, res.Parts[1].Outcome)
		assert.Equal(t, core.OutcomeDone, res.Parts[2].Outcome)
	})
	t.Run("the caller goes: the parts not started say so", func(t *testing.T) {
		s, _, w := runSession(t, twoPods()...)
		plan := drainPlan(t, s)
		ctx, cancel := context.WithCancel(context.Background())
		w.answer = func(context.Context, int, evictCall) error {
			cancel() // after the first eviction is sent
			return nil
		}
		res, err := s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "drain", Expect: plan.Expect})
		require.NoError(t, err)
		assert.Equal(t, core.OutcomeDone, res.Parts[1].Outcome)
		assert.Equal(t, "kubernetes.drain.why.cancelled", res.Parts[2].Why.Key)
		assert.Len(t, w.calls(), 1)
	})
}
