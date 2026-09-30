package kubernetes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

var jobsGVR = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}

// jobRec records the Jobs created (a fake writer): each create is counted;
// answer (if set) answers instead of the fake client; hold (if set) is
// waited for before answering.
type jobRec struct {
	drainRec
	mu      sync.Mutex
	created []map[string]any
	answer  func(n int) error
	hold    chan struct{}
}

func (w *jobRec) create(ctx context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured) error {
	w.mu.Lock()
	w.created = append(w.created, obj.DeepCopy().Object)
	n, f, h := len(w.created), w.answer, w.hold
	w.mu.Unlock()
	if h != nil {
		<-h
	}
	if f != nil {
		if err := f(n); err != nil {
			return err
		}
	}
	_, err := w.dyn.Resource(gvr).Namespace(ns).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

func (w *jobRec) creates() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.created)
}

func runSessionFor(t *testing.T, objs ...*unstructured.Unstructured) (*session, *jobRec) {
	t.Helper()
	s := cronJobSession(t, cronJobsV1, objs...)
	w := &jobRec{drainRec: drainRec{dyn: s.dyn}}
	s.writer = w
	return s, w
}

func runNow(s *session, plan core.ActionPlan) (core.ActionResult, error) {
	return runNowCtx(context.Background(), s, plan)
}

func runNowCtx(ctx context.Context, s *session, plan core.ActionPlan) (core.ActionResult, error) {
	return s.RunAction(ctx, provider.ActionRun{Ref: plan.Where.Ref, Action: "run", Expect: plan.Expect})
}

func classOf(t *testing.T, err error) provider.ErrorClass {
	t.Helper()
	var pe *provider.Error
	require.True(t, errors.As(err, &pe), "%v", err)
	return pe.Class
}

// jobOf: the Job name a run plan shows.
func jobOf(t *testing.T, plan core.ActionPlan) string {
	t.Helper()
	for _, e := range plan.Effects {
		if e.Key == "kubernetes.cronjob.run" {
			return e.Params["job"]
		}
	}
	require.Fail(t, "no run effect", "%v", plan.Effects)
	return ""
}

func TestRunNowJobNames(t *testing.T) {
	for _, tc := range []struct{ cron, want string }{
		{"nightly", "nightly-manual-x7k2p"},
		{"db.backup", "db.backup-manual-x7k2p"},
		{strings.Repeat("a", 52), strings.Repeat("a", 50) + "-manual-x7k2p"},
		{strings.Repeat("a", 49) + ".b", strings.Repeat("a", 49) + "-manual-x7k2p"},  // a dot at the cut
		{strings.Repeat("a", 49) + "-bc", strings.Repeat("a", 49) + "-manual-x7k2p"}, // a hyphen at the cut
		{strings.Repeat("a", 48) + ".a-b", strings.Repeat("a", 48) + ".a-manual-x7k2p"},
		{strings.Repeat("a", 50) + "-b", strings.Repeat("a", 50) + "-manual-x7k2p"},
	} {
		t.Run(tc.cron, func(t *testing.T) {
			require.Empty(t, validation.IsDNS1123Subdomain(tc.cron), "a valid CronJob name")
			got, ok := runJobName(tc.cron, "x7k2p")
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), 63)
			assert.Empty(t, validation.IsDNS1123Subdomain(got))
			assert.Empty(t, validation.IsValidLabelValue(got))
		})
	}
}

func TestRunNowReview(t *testing.T) {
	t.Run("the Job to be created is named, its fate said", func(t *testing.T) {
		s, _ := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		assert.Nil(t, plan.Unavailable)
		assert.False(t, plan.Destructive)
		assert.Regexp(t, `^nightly-manual-[a-z0-9]{5}$`, jobOf(t, plan))
		assert.Equal(t, []string{"kubernetes.cronjob.run", "kubernetes.cronjob.runOwned", "kubernetes.cronjob.runConcurrency", "kubernetes.cronjob.runUnexpected"}, effectKeys(plan))
		assert.Equal(t, "3", plan.Effects[1].Params["succeeded"], "the default history limits")
		assert.Equal(t, "1", plan.Effects[1].Params["failed"])
		assert.Equal(t, "Allow", plan.Effects[2].Params["policy"])
		assert.Empty(t, plan.Warnings)
	})
	t.Run("two reviews name two Jobs", func(t *testing.T) {
		s, _ := runSessionFor(t, cronJob("nightly"))
		a := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		b := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		assert.NotEqual(t, jobOf(t, a), jobOf(t, b))
	})
	t.Run("warnings: suspended, a suspended template, no parallelism, runs seen active", func(t *testing.T) {
		tmpl := func(o map[string]any) {
			spec := o["spec"].(map[string]any)["jobTemplate"].(map[string]any)["spec"].(map[string]any)
			spec["suspend"], spec["parallelism"] = true, int64(0)
		}
		s, _ := runSessionFor(t, cronJob("nightly", suspended, tmpl, activeRuns("nightly-1")))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		var keys []string
		for _, w := range plan.Warnings {
			keys = append(keys, w.Key)
		}
		assert.Equal(t, []string{"kubernetes.cronjob.runSuspended", "kubernetes.cronjob.runTemplateSuspended", "kubernetes.cronjob.runNoParallelism", "kubernetes.cronjob.runAlongsideOne"}, keys)
	})
	t.Run("the right asked: create jobs in the namespace", func(t *testing.T) {
		s, _ := runSessionFor(t, cronJob("nightly"))
		var r ssarRules
		r.install(s.dyn.(*dynamicfake.FakeDynamicClient), func(map[string]any) bool { return true })
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		assert.Equal(t, core.RightsAllowed, plan.Rights.State)
		asked := r.askedAbout("jobs")
		require.Len(t, asked, 1)
		assert.Equal(t, map[string]any{"verb": "create", "group": "batch", "resource": "jobs", "namespace": "ns"}, asked[0])
	})
}

func TestRunNowCreatesTheReviewedJob(t *testing.T) {
	annotated := func(o map[string]any) {
		md := o["spec"].(map[string]any)["jobTemplate"].(map[string]any)["metadata"].(map[string]any)
		md["annotations"] = map[string]any{"team": "a"}
	}
	s, w := runSessionFor(t, cronJob("nightly", annotated))
	plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
	job := jobOf(t, plan)
	res, err := runNow(s, plan)
	require.NoError(t, err)
	assert.Contains(t, res.Message, job)
	require.Equal(t, 1, w.creates())
	tmpl := cronJob("nightly").Object["spec"].(map[string]any)["jobTemplate"].(map[string]any)["spec"]
	assert.Equal(t, map[string]any{
		"apiVersion": "batch/v1", "kind": "Job",
		"metadata": map[string]any{
			"name": job, "namespace": "ns",
			"labels":          map[string]any{"app": "nightly"},
			"annotations":     map[string]any{"cronjob.kubernetes.io/instantiate": "manual", "team": "a"},
			"ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "CronJob", "name": "nightly", "uid": "uid-nightly", "controller": true}},
		},
		"spec": tmpl,
	}, w.created[0])
	_, err = s.dyn.Resource(jobsGVR).Namespace("ns").Get(context.Background(), job, metav1.GetOptions{})
	require.NoError(t, err)
}

// As kubectl: the template's own instantiate annotation wins.
func TestRunNowKeepsTheTemplatesAnnotations(t *testing.T) {
	s, w := runSessionFor(t, cronJob("nightly", func(o map[string]any) {
		md := o["spec"].(map[string]any)["jobTemplate"].(map[string]any)["metadata"].(map[string]any)
		md["annotations"] = map[string]any{"cronjob.kubernetes.io/instantiate": "scheduled"}
	}))
	_, err := runNow(s, prepare(t, s, cronRef("nightly"), "run", core.ActionParams{}))
	require.NoError(t, err)
	md := w.created[0]["metadata"].(map[string]any)
	assert.Equal(t, map[string]any{"cronjob.kubernetes.io/instantiate": "scheduled"}, md["annotations"])
}

// grantParts: the route, payload and signature of a run plan's Expect.
func grantParts(t *testing.T, expect string) (route string, payload map[string]any, sig string) {
	t.Helper()
	route, rest, ok := strings.Cut(expect, "-")
	require.True(t, ok)
	enc, sig, ok := strings.Cut(rest, ".")
	require.True(t, ok)
	b, err := base64.RawURLEncoding.DecodeString(enc)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &payload))
	return route, payload, sig
}

func withPayload(t *testing.T, expect string, change func(p map[string]any)) string {
	t.Helper()
	route, p, sig := grantParts(t, expect)
	change(p)
	b, err := json.Marshal(p)
	require.NoError(t, err)
	return route + "-" + base64.RawURLEncoding.EncodeToString(b) + "." + sig
}

func TestRunNowGrant(t *testing.T) {
	t.Run("the name, the object or the expiry changed under the same signature: invalid, nothing created", func(t *testing.T) {
		for field, v := range map[string]any{"job": "nightly-manual-zzzzz", "uid": "uid-other", "exp": float64(time.Now().Add(time.Hour).UnixMilli()), "name": "other"} {
			t.Run(field, func(t *testing.T) {
				s, w := runSessionFor(t, cronJob("nightly"))
				plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
				plan.Expect = withPayload(t, plan.Expect, func(p map[string]any) { p[field] = v })
				_, err := runNow(s, plan)
				assert.Equal(t, provider.ClassInvalid, classOf(t, err))
				assert.Zero(t, w.creates())
			})
		}
	})
	t.Run("a foreign signature: invalid", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		i := len(plan.Expect) - 1
		flip := byte('0')
		if plan.Expect[i] == '0' {
			flip = '1'
		}
		plan.Expect = plan.Expect[:i] + string(flip)
		_, err := runNow(s, plan)
		assert.Equal(t, provider.ClassInvalid, classOf(t, err))
		assert.Zero(t, w.creates())
	})
	t.Run("another object's grant: invalid", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"), cronJob("hourly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		other := prepare(t, s, cronRef("hourly"), "run", core.ActionParams{})
		plan.Expect = other.Expect
		_, err := runNow(s, plan)
		assert.Equal(t, provider.ClassInvalid, classOf(t, err))
		assert.Zero(t, w.creates())
	})
	t.Run("expired: review again", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		s.now = func() time.Time { return time.Now().Add(runGrantTTL + time.Second) }
		_, err := runNow(s, plan)
		assert.Equal(t, provider.ClassConflict, classOf(t, err))
		assert.Zero(t, w.creates())
	})
	t.Run("expiring during the run's read: nothing created", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		s.beforeWrite = func(string, *unstructured.Unstructured) {
			s.now = func() time.Time { return time.Now().Add(runGrantTTL + time.Second) }
		}
		_, err := runNow(s, plan)
		assert.Equal(t, provider.ClassConflict, classOf(t, err))
		assert.Zero(t, w.creates())
	})
	t.Run("another session incarnation: review again", func(t *testing.T) {
		s, _ := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		s2, w2 := runSessionFor(t, cronJob("nightly"))
		_, err := runNow(s2, plan)
		assert.Equal(t, provider.ClassConflict, classOf(t, err))
		assert.Zero(t, w2.creates())
	})
	t.Run("run again after the Job was created and deleted: refused, one Job", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		_, err := runNow(s, plan)
		require.NoError(t, err)
		require.NoError(t, s.dyn.Resource(jobsGVR).Namespace("ns").Delete(context.Background(), jobOf(t, plan), metav1.DeleteOptions{}))
		_, err = runNow(s, plan)
		assert.Equal(t, provider.ClassConflict, classOf(t, err))
		assert.Equal(t, 1, w.creates())
	})
	t.Run("run again after an unknown outcome: refused", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		w.answer = func(int) error { return apierrors.NewServiceUnavailable("down") }
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		_, err := runNow(s, plan)
		assert.Equal(t, provider.ClassUnknown, classOf(t, err))
		_, err = runNow(s, plan)
		assert.Equal(t, provider.ClassConflict, classOf(t, err))
		assert.Equal(t, 1, w.creates())
	})
	t.Run("two runs of one grant at once: one Job", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		w.hold = make(chan struct{})
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		errs := make(chan error, 2)
		for range 2 {
			go func() { _, err := runNow(s, plan); errs <- err }()
		}
		var first error
		select {
		case first = <-errs: // the one refused while the other is held
		case <-time.After(5 * time.Second):
			close(w.hold)
			require.Fail(t, "both runs reached the write")
		}
		assert.Equal(t, provider.ClassConflict, classOf(t, first))
		close(w.hold)
		require.NoError(t, <-errs)
		assert.Equal(t, 1, w.creates())
	})
	t.Run("cancelled before the write: nothing created, the grant not spent", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		ctx, cancel := context.WithCancel(context.Background())
		s.beforeWrite = func(string, *unstructured.Unstructured) { cancel() }
		_, err := runNowCtx(ctx, s, plan)
		require.Error(t, err)
		assert.Zero(t, w.creates())
		s.beforeWrite = nil
		_, err = runNow(s, plan)
		require.NoError(t, err)
		assert.Equal(t, 1, w.creates())
	})
	t.Run("spent grants are forgotten when they expire, and bounded", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		old := s.now().Add(-time.Minute)
		for i := range maxSpentGrants {
			s.spent.byNonce[fmt.Sprint("old-", i)] = old
		}
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		_, err := runNow(s, plan)
		require.NoError(t, err)
		assert.Len(t, s.spent.byNonce, 1, "the expired ones pruned")

		live := s.now().Add(time.Minute)
		for i := range maxSpentGrants {
			s.spent.byNonce[fmt.Sprint("live-", i)] = live
		}
		plan = prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		_, err = runNow(s, plan)
		assert.Equal(t, provider.ClassUnavailable, classOf(t, err))
		assert.Equal(t, 1, w.creates())
	})
}

func TestRunNowChecksWhatItsTextsRead(t *testing.T) {
	for name, change := range map[string]func(o map[string]any){
		"the template": func(o map[string]any) {
			o["spec"].(map[string]any)["jobTemplate"].(map[string]any)["metadata"] = map[string]any{"labels": map[string]any{"app": "other"}}
		},
		"suspended":         suspended,
		"concurrencyPolicy": func(o map[string]any) { o["spec"].(map[string]any)["concurrencyPolicy"] = "Forbid" },
		"a history limit":   func(o map[string]any) { o["spec"].(map[string]any)["successfulJobsHistoryLimit"] = int64(0) },
	} {
		t.Run(name+" changed: a conflict, nothing created", func(t *testing.T) {
			s, w := runSessionFor(t, cronJob("nightly"))
			plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
			now := cronJob("nightly", change)
			now.SetResourceVersion("6")
			_, err := s.dyn.Resource(cronJobsGVR).Namespace("ns").Update(context.Background(), now, metav1.UpdateOptions{})
			require.NoError(t, err)
			_, err = runNow(s, plan)
			assert.Equal(t, provider.ClassConflict, classOf(t, err))
			assert.Zero(t, w.creates())
		})
	}
	t.Run("only the status changed: it runs", func(t *testing.T) {
		s, w := runSessionFor(t, cronJob("nightly"))
		plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
		now := cronJob("nightly", activeRuns("nightly-1"))
		now.SetResourceVersion("6")
		_, err := s.dyn.Resource(cronJobsGVR).Namespace("ns").Update(context.Background(), now, metav1.UpdateOptions{})
		require.NoError(t, err)
		_, err = runNow(s, plan)
		require.NoError(t, err)
		assert.Equal(t, 1, w.creates())
	})
}

func TestRunNowAnswers(t *testing.T) {
	gr := schema.GroupResource{Group: "batch", Resource: "jobs"}
	for _, tc := range []struct {
		name  string
		err   error
		class provider.ErrorClass
		text  string
	}{
		{"already there", apierrors.NewAlreadyExists(gr, "x"), provider.ClassConflict, "already exists"},
		{"forbidden", apierrors.NewForbidden(gr, "x", errors.New("rbac")), provider.ClassForbidden, ""},
		{"invalid", apierrors.NewInvalid(schema.GroupKind{Group: "batch", Kind: "Job"}, "x", nil), provider.ClassInvalid, ""},
		{"server error", apierrors.NewInternalError(errors.New("boom")), provider.ClassUnknown, "before repeating"},
		{"no answer", errors.New("connection reset"), provider.ClassUnknown, "before repeating"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, w := runSessionFor(t, cronJob("nightly"))
			w.answer = func(int) error { return tc.err }
			plan := prepare(t, s, cronRef("nightly"), "run", core.ActionParams{})
			_, err := runNow(s, plan)
			assert.Equal(t, tc.class, classOf(t, err))
			assert.Contains(t, err.Error(), tc.text)
			if tc.class == provider.ClassUnknown {
				assert.Contains(t, err.Error(), jobOf(t, plan), "the name to look for")
			}
			assert.Equal(t, 1, w.creates(), "never tried again")
		})
	}
}
