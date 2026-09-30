package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var (
	cronJobsV1  = v2ver{version: "v1", res: []v2res{{name: "cronjobs", kind: "CronJob", singular: "cronjob", scope: "Namespaced", verbs: append([]string{"patch"}, lw...), short: []string{"cj"}}}}
	cronJobsGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}
)

// cronJob: a CronJob in ns "ns" at resourceVersion "5"; opts change it.
func cronJob(name string, opts ...func(o map[string]any)) *unstructured.Unstructured {
	o := map[string]any{
		"apiVersion": "batch/v1", "kind": "CronJob",
		"metadata": map[string]any{"name": name, "namespace": "ns", "uid": "uid-" + name, "resourceVersion": "5", "creationTimestamp": "2026-09-30T10:00:00Z"},
		"spec": map[string]any{
			"schedule": "0 0 1 1 *",
			"jobTemplate": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": name}},
				"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
					"restartPolicy": "Never",
					"containers":    []any{map[string]any{"name": "app", "image": "busybox:1.36", "command": []any{"true"}}},
				}}},
			},
		},
	}
	for _, f := range opts {
		f(o)
	}
	return &unstructured.Unstructured{Object: o}
}

func suspended(o map[string]any) { o["spec"].(map[string]any)["suspend"] = true }

func startingDeadline(seconds int64) func(o map[string]any) {
	return func(o map[string]any) { o["spec"].(map[string]any)["startingDeadlineSeconds"] = seconds }
}

func activeRuns(names ...string) func(o map[string]any) {
	return func(o map[string]any) {
		var a []any
		for _, n := range names {
			a = append(a, map[string]any{"kind": "Job", "name": n, "namespace": "ns"})
		}
		o["status"] = map[string]any{"active": a}
	}
}

// cronJobSession: a session whose catalog serves batch/v1 cronjobs (a
// discovered kind), with objs.
func cronJobSession(t *testing.T, version v2ver, objs ...*unstructured.Unstructured) *session {
	t.Helper()
	api := &scriptedAPI{}
	api.set("/api", coreDoc)
	api.set("/apis", apisDoc(map[string][]v2ver{"batch": {version}}, "batch"))
	var ro []runtime.Object
	for _, o := range objs {
		ro = append(ro, o)
	}
	s := newSession("ctx", "h", catalogClient(ro...), false)
	t.Cleanup(s.Close)
	s.startCatalog(api.get)
	waitRev(t, s.cat, 2)
	return s
}

func cronRef(name string) core.Ref { return core.Ref{Kind: "batch/cronjobs", Scope: "ns", Name: name} }

func actionIDs(s *session, kind string) []string {
	var ids []string
	for _, d := range s.Kinds() {
		if d.ID == kind {
			for _, a := range d.Actions {
				ids = append(ids, a.ID)
			}
		}
	}
	return ids
}

func TestCronJobsGetTheirActions(t *testing.T) {
	t.Run("batch/v1 with patch", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1)
		assert.Equal(t, []string{"suspend", "resume", "run", "delete"}, actionIDs(s, "batch/cronjobs"))
	})
	t.Run("without patch: no suspend or resume", func(t *testing.T) {
		v := cronJobsV1
		v.res = []v2res{v.res[0]}
		v.res[0].verbs = lw
		s := cronJobSession(t, v)
		assert.Equal(t, []string{"run", "delete"}, actionIDs(s, "batch/cronjobs"))
	})
	t.Run("another version: only delete", func(t *testing.T) {
		v := cronJobsV1
		v.version = "v2alpha1"
		s := cronJobSession(t, v)
		assert.Equal(t, []string{"delete"}, actionIDs(s, "batch/cronjobs"))
	})
	t.Run("other discovered kinds keep delete only", func(t *testing.T) {
		s, _ := widgetSession(t)
		assert.Equal(t, []string{"delete"}, actionIDs(s, "ocular.dev/widgets"))
	})
}

func effectKeys(plan core.ActionPlan) []string {
	var out []string
	for _, m := range plan.Effects {
		out = append(out, m.Key)
	}
	return out
}

func TestCronJobSuspendAndResumeReview(t *testing.T) {
	t.Run("suspend: runs stop, running Jobs go on, the active runs seen", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", activeRuns("nightly-1", "nightly-2")))
		plan := prepare(t, s, cronRef("nightly"), "suspend", core.ActionParams{})
		assert.Nil(t, plan.Unavailable)
		assert.False(t, plan.Destructive)
		assert.Equal(t, []string{"kubernetes.cronjob.suspend", "kubernetes.cronjob.activeSeen"}, effectKeys(plan))
		assert.Equal(t, "2", plan.Effects[1].Params["count"])
	})
	t.Run("suspend of a suspended CronJob is unavailable", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", suspended))
		plan := prepare(t, s, cronRef("nightly"), "suspend", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.unavailable.suspended", plan.Unavailable.Key)
	})
	t.Run("resume of a running CronJob is unavailable", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "resume", core.ActionParams{})
		require.NotNil(t, plan.Unavailable)
		assert.Equal(t, "kubernetes.unavailable.notSuspended", plan.Unavailable.Key)
	})
	t.Run("resume without a starting deadline: a missed run may start at once", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", suspended))
		plan := prepare(t, s, cronRef("nightly"), "resume", core.ActionParams{})
		assert.Nil(t, plan.Unavailable)
		assert.Equal(t, []string{"kubernetes.cronjob.resume", "kubernetes.cronjob.missed"}, effectKeys(plan))
	})
	t.Run("resume with a starting deadline says it", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", suspended, startingDeadline(300)))
		plan := prepare(t, s, cronRef("nightly"), "resume", core.ActionParams{})
		assert.Equal(t, []string{"kubernetes.cronjob.resume", "kubernetes.cronjob.missedDeadline"}, effectKeys(plan))
		assert.Equal(t, "300", plan.Effects[1].Params["seconds"])
	})
}

// patchRec records merge patches (a fake writer).
type patchRec struct {
	drainRec
	bodies []map[string]any
}

func (w *patchRec) patch(ctx context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, sub string) error {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	w.bodies = append(w.bodies, m)
	return w.drainRec.patch(ctx, gvr, ns, name, pt, data, sub)
}

func TestCronJobSuspendAndResumeRun(t *testing.T) {
	run := func(s *session, plan core.ActionPlan) (core.ActionResult, error) {
		return s.RunAction(context.Background(), provider.ActionRun{Ref: plan.Where.Ref, Action: plan.Action.ID, Expect: plan.Expect})
	}
	t.Run("suspend: one merge patch with uid and version", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly"))
		w := &patchRec{drainRec: drainRec{dyn: s.dyn}}
		s.writer = w
		plan := prepare(t, s, cronRef("nightly"), "suspend", core.ActionParams{})
		_, err := run(s, plan)
		require.NoError(t, err)
		require.Len(t, w.bodies, 1)
		assert.Equal(t, map[string]any{
			"metadata": map[string]any{"uid": "uid-nightly", "resourceVersion": "5"},
			"spec":     map[string]any{"suspend": true},
		}, w.bodies[0])
		u, err := s.dyn.Resource(cronJobsGVR).Namespace("ns").Get(context.Background(), "nightly", metav1.GetOptions{})
		require.NoError(t, err)
		assert.True(t, boolAt(u.Object, "spec", "suspend"))
	})
	t.Run("resume writes false", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", suspended))
		w := &patchRec{drainRec: drainRec{dyn: s.dyn}}
		s.writer = w
		plan := prepare(t, s, cronRef("nightly"), "resume", core.ActionParams{})
		_, err := run(s, plan)
		require.NoError(t, err)
		require.Len(t, w.bodies, 1)
		assert.Equal(t, false, w.bodies[0]["spec"].(map[string]any)["suspend"])
	})
	t.Run("suspended by someone else after the review: a conflict, nothing written", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly"))
		w := &patchRec{drainRec: drainRec{dyn: s.dyn}}
		s.writer = w
		plan := prepare(t, s, cronRef("nightly"), "suspend", core.ActionParams{})
		c := s.dyn.Resource(cronJobsGVR).Namespace("ns")
		now := cronJob("nightly", suspended)
		now.SetResourceVersion("6")
		_, err := c.Update(context.Background(), now, metav1.UpdateOptions{})
		require.NoError(t, err)
		_, err = run(s, plan)
		var pe *provider.Error
		require.True(t, errors.As(err, &pe))
		assert.Equal(t, provider.ClassConflict, pe.Class)
		assert.Empty(t, w.bodies)
	})
	t.Run("a new starting deadline after a resume review: a conflict", func(t *testing.T) {
		s := cronJobSession(t, cronJobsV1, cronJob("nightly", suspended))
		w := &patchRec{drainRec: drainRec{dyn: s.dyn}}
		s.writer = w
		plan := prepare(t, s, cronRef("nightly"), "resume", core.ActionParams{})
		now := cronJob("nightly", suspended, startingDeadline(60))
		now.SetResourceVersion("6")
		_, err := s.dyn.Resource(cronJobsGVR).Namespace("ns").Update(context.Background(), now, metav1.UpdateOptions{})
		require.NoError(t, err)
		_, err = run(s, plan)
		var pe *provider.Error
		require.True(t, errors.As(err, &pe))
		assert.Equal(t, provider.ClassConflict, pe.Class)
		assert.Empty(t, w.bodies)
	})
}
