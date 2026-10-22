package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/krr"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kt "k8s.io/client-go/testing"
)

type krrTestJournal struct {
	record             core.OwnedHelper
	uid                string
	finished, clean    bool
	failBegin, failPin bool
}

func (j *krrTestJournal) Begin(_ context.Context, r core.OwnedHelper, _ context.CancelFunc) (provider.HelperLease, error) {
	if j.failBegin {
		return nil, errors.New("fixture")
	}
	j.record = r
	return j, nil
}
func (j *krrTestJournal) PinUID(_ context.Context, uid string) error {
	if j.failPin {
		return errors.New("fixture")
	}
	j.uid = uid
	return nil
}
func (j *krrTestJournal) Finish(_ context.Context, clean bool) error {
	j.finished = true
	j.clean = clean
	return nil
}
func krrTestSession(t *testing.T) (*session, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	ns := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "tools", "uid": "ns-uid"}}}
	sa := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "reader", "namespace": "tools", "uid": "sa-uid"}}}
	c := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(kruntime.NewScheme(), map[schema.GroupVersionResource]string{krrJobs: "JobList", podsKind.gvr: "PodList", namespaceGV: "NamespaceList", krrAccounts: "ServiceAccountList"}, ns, sa)
	ssarAnswer(c, true, nil)
	s := newSession("fixture", "hash", c, false)
	t.Cleanup(s.Close)
	b := krrProtocolReport(t, "tools")
	s.logs = func(_ context.Context, r podLogRequest) (io.ReadCloser, error) {
		require.Equal(t, int64(krr.MaxBytes+128), r.LimitBytes)
		require.False(t, r.Follow)
		return io.NopCloser(strings.NewReader("2026-10-22T00:00:00Z " + strings.TrimSpace(string(b)) + "\n")), nil
	}
	return s, c
}
func krrTestOptions() krr.RunOptions {
	return krr.RunOptions{Namespace: "tools", ServiceAccount: "reader", Source: "http://metrics.tools.svc:9090", SingleCluster: true, ReviewedAccount: true, NoSourceAuth: true}
}
func krrPrepared(t *testing.T, s *session, j *krrTestJournal) *krrExecution {
	t.Helper()
	r, e := s.ReviewKRR(t.Context(), krrTestOptions())
	require.NoError(t, e)
	h, e := s.PrepareKRR(t.Context(), r, j)
	require.NoError(t, e)
	return h.(*krrExecution)
}
func krrReady(c *dynamicfake.FakeDynamicClient, j *krrTestJournal, mode string) {
	c.PrependReactor("create", "jobs", func(a kt.Action) (bool, kruntime.Object, error) {
		if j.record.ID == "" {
			return true, nil, errors.New("intent missing")
		}
		job := a.(kt.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		job.SetUID("job-uid")
		job.SetResourceVersion("1")
		if mode == "changed" {
			containers, _, _ := unstructured.NestedSlice(job.Object, "spec", "template", "spec", "containers")
			containers[0].(map[string]any)["image"] = "other:latest"
			_ = unstructured.SetNestedSlice(job.Object, containers, "spec", "template", "spec", "containers")
		}
		e := c.Tracker().Create(krrJobs, job, job.GetNamespace())
		if mode == "ambiguous" {
			return true, nil, io.ErrUnexpectedEOF
		}
		return true, job, e
	})
	c.PrependReactor("patch", "jobs", func(a kt.Action) (bool, kruntime.Object, error) {
		if j.uid != "job-uid" {
			return true, nil, errors.New("UID not pinned")
		}
		job, e := c.Tracker().Get(krrJobs, "tools", a.(kt.PatchAction).GetName())
		if e != nil {
			return true, nil, e
		}
		u := job.(*unstructured.Unstructured).DeepCopy()
		_ = unstructured.SetNestedField(u.Object, false, "spec", "suspend")
		u.SetResourceVersion("2")
		e = c.Tracker().Update(krrJobs, u, "tools")
		spec, _, _ := unstructured.NestedMap(u.Object, "spec", "template", "spec")
		controller := true
		pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": u.GetName() + "-pod", "namespace": "tools", "uid": "pod-uid", "labels": map[string]any{krrJobLabel: j.record.ID}}, "spec": spec, "status": map[string]any{"phase": "Succeeded", "containerStatuses": []any{map[string]any{"name": "krr", "state": map[string]any{"terminated": map[string]any{"exitCode": int64(0)}}}}}}}
		pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: u.GetName(), UID: u.GetUID(), Controller: &controller}})
		if e == nil {
			e = c.Tracker().Create(podsKind.gvr, pod, "tools")
		}
		return true, u, e
	})
}
func TestKRRJobJournalReportAndUIDCleanup(t *testing.T) {
	s, c := krrTestSession(t)
	j := &krrTestJournal{}
	h := krrPrepared(t, s, j)
	krrReady(c, j, "")
	data, e := h.Execute(t.Context())
	require.NoError(t, e)
	require.NotEmpty(t, data)
	require.True(t, j.finished)
	require.True(t, j.clean)
	require.Equal(t, "krr-job", j.record.Kind)
	require.Empty(t, j.record.NodeUID)
	require.Equal(t, "sa-uid", j.record.ServiceAccountUID)
	require.Len(t, j.record.ContractSHA, 64)
	require.NotContains(t, j.record.ContractSHA, "metrics")
	_, e = c.Resource(krrJobs).Namespace("tools").Get(t.Context(), h.g.Name, metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(e))
	pods, e := s.krrPods(t.Context(), "tools")
	require.NoError(t, e)
	require.Empty(t, pods)
	for _, a := range c.Actions() {
		if a.GetVerb() == "delete" {
			opts := a.(kt.DeleteAction).GetDeleteOptions()
			require.NotNil(t, opts.Preconditions)
			require.NotNil(t, opts.Preconditions.UID)
			require.Contains(t, []types.UID{"job-uid", "pod-uid"}, *opts.Preconditions.UID)
		}
	}
	_, e = h.Execute(t.Context())
	require.Error(t, e)
}
func TestKRRJobReviewFencesAndNoWrites(t *testing.T) {
	for _, mode := range []string{"token", "options", "image", "namespace", "account", "session", "expiry", "replay"} {
		t.Run(mode, func(t *testing.T) {
			s, c := krrTestSession(t)
			r, e := s.ReviewKRR(t.Context(), krrTestOptions())
			require.NoError(t, e)
			j := &krrTestJournal{}
			switch mode {
			case "token":
				r.Expect += "x"
			case "options":
				r.Options.Source = "http://other.invalid"
			case "image":
				r.Image = "other"
			case "namespace":
				u, e := c.Resource(namespaceGV).Get(t.Context(), "tools", metav1.GetOptions{})
				require.NoError(t, e)
				u.SetUID("replacement")
				_, e = c.Resource(namespaceGV).Update(t.Context(), u, metav1.UpdateOptions{})
				require.NoError(t, e)
			case "account":
				u, e := c.Resource(krrAccounts).Namespace("tools").Get(t.Context(), "reader", metav1.GetOptions{})
				require.NoError(t, e)
				u.SetUID("replacement")
				_, e = c.Resource(krrAccounts).Namespace("tools").Update(t.Context(), u, metav1.UpdateOptions{})
				require.NoError(t, e)
			case "session":
				s.incarnation = "replacement"
			case "expiry":
				s.now = func() time.Time { return time.Now().Add(time.Hour) }
			case "replay":
				_, e = s.PrepareKRR(t.Context(), r, j)
				require.NoError(t, e)
			}
			_, e = s.PrepareKRR(t.Context(), r, j)
			require.Error(t, e)
			for _, a := range c.Actions() {
				if a.GetResource() != ssarGVR {
					require.NotEqual(t, "create", a.GetVerb())
				}
				require.NotEqual(t, "patch", a.GetVerb())
				require.NotEqual(t, "delete", a.GetVerb())
			}
		})
	}
}
func TestKRRJobAmbiguousCreateAndAdmissionChange(t *testing.T) {
	for _, mode := range []string{"ambiguous", "changed", "journal"} {
		t.Run(mode, func(t *testing.T) {
			s, c := krrTestSession(t)
			j := &krrTestJournal{failBegin: mode == "journal"}
			h := krrPrepared(t, s, j)
			krrReady(c, j, mode)
			_, e := h.Execute(t.Context())
			require.Error(t, e)
			creates, patches := 0, 0
			for _, a := range c.Actions() {
				if a.GetVerb() == "create" && a.GetResource() == krrJobs {
					creates++
				}
				if a.GetVerb() == "patch" {
					patches++
				}
			}
			require.Zero(t, patches)
			if mode == "journal" {
				require.Zero(t, creates)
			} else {
				require.Equal(t, 1, creates)
				require.True(t, j.clean)
				require.Equal(t, "job-uid", j.uid)
			}
		})
	}
}
func TestKRRJobCleanupParentGoneAndForeignReplacement(t *testing.T) {
	s, c := krrTestSession(t)
	j := &krrTestJournal{}
	h := krrPrepared(t, s, j)
	r := h.g.identity()
	r.Target = s.conn.target
	r.UID = "old-job"
	foreign := h.g.job()
	foreign.SetUID("new-job")
	require.NoError(t, c.Tracker().Create(krrJobs, foreign, "tools"))
	controller := false
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "old-child", "namespace": "tools", "uid": "old-child-uid"}}}
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: r.Name, UID: types.UID(r.UID), Controller: &controller}})
	require.NoError(t, c.Tracker().Create(podsKind.gvr, pod, "tools"))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	clean, e := s.cleanupKRRJob(ctx, r, nil)
	require.NoError(t, e)
	require.True(t, clean)
	kept, e := c.Resource(krrJobs).Namespace("tools").Get(t.Context(), r.Name, metav1.GetOptions{})
	require.NoError(t, e)
	require.Equal(t, types.UID("new-job"), kept.GetUID())
	for _, a := range c.Actions() {
		if a.GetVerb() == "delete" {
			require.Equal(t, podsKind.gvr, a.GetResource())
		}
	}
	// Parent 404 with a still-live child is also cleaned, even with no labels.
	require.NoError(t, c.Tracker().Delete(krrJobs, "tools", r.Name))
	require.NoError(t, c.Tracker().Create(podsKind.gvr, pod, "tools"))
	clean, e = s.cleanupKRRJob(ctx, r, nil)
	require.NoError(t, e)
	require.True(t, clean)
}
func TestKRRJobRecoveryRejectsCopiedLabelsAndUnknownKind(t *testing.T) {
	s, c := krrTestSession(t)
	h := krrPrepared(t, s, &krrTestJournal{})
	r := h.g.identity()
	r.Target = s.conn.target
	foreign := h.g.job()
	foreign.SetUID("foreign")
	_ = unstructured.SetNestedField(foreign.Object, "changed", "spec", "template", "spec", "serviceAccountName")
	require.NoError(t, c.Tracker().Create(krrJobs, foreign, "tools"))
	clean, e := s.CleanupOwnedHelper(t.Context(), r, func(string) error { t.Fatal("must not pin foreign contract"); return nil })
	require.ErrorIs(t, e, krr.ErrCleanup)
	require.False(t, clean)
	r.Kind = "unknown"
	clean, e = s.CleanupOwnedHelper(t.Context(), r, nil)
	require.Error(t, e)
	require.False(t, clean)
	for _, a := range c.Actions() {
		require.NotEqual(t, "delete", a.GetVerb())
	}
}

func TestKRRJobCleanupFailureRetainsReportAndRecovery(t *testing.T) {
	s, c := krrTestSession(t)
	journal := &krrTestJournal{}
	h := krrPrepared(t, s, journal)
	krrReady(c, journal, "")
	c.PrependReactor("delete", "jobs", func(kt.Action) (bool, kruntime.Object, error) {
		return true, nil, apierrors.NewForbidden(krrJobs.GroupResource(), h.g.Name, errors.New("PRIVATE-error"))
	})
	data, e := h.Execute(t.Context())
	require.ErrorIs(t, e, krr.ErrCleanup)
	require.NotContains(t, e.Error(), "PRIVATE")
	require.NotEmpty(t, data)
	require.True(t, journal.finished)
	require.False(t, journal.clean)
}
func TestKRRJobCleanupUnknownParentUIDNeverUsesLabelsAsOwnership(t *testing.T) {
	s, c := krrTestSession(t)
	h := krrPrepared(t, s, &krrTestJournal{})
	r := h.g.identity()
	r.Target = s.conn.target
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "copied-label", "namespace": "tools", "uid": "foreign", "labels": map[string]any{krrJobLabel: r.ID}}}}
	require.NoError(t, c.Tracker().Create(podsKind.gvr, pod, "tools"))
	clean, e := s.cleanupKRRJob(t.Context(), r, nil)
	require.False(t, clean)
	require.ErrorIs(t, e, krr.ErrCleanup)
	// Original labels may have disappeared after parent GC. Owner name alone is
	// insufficient to delete, but sufficient to keep absence unconfirmed.
	pod.SetLabels(nil)
	pod.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: r.Name, UID: "unobserved-job"}})
	require.NoError(t, c.Tracker().Update(podsKind.gvr, pod, "tools"))
	clean, e = s.cleanupKRRJob(t.Context(), r, nil)
	require.False(t, clean)
	require.ErrorIs(t, e, krr.ErrCleanup)
	for _, a := range c.Actions() {
		require.NotEqual(t, "delete", a.GetVerb())
	}
}

func TestKRRJobRequiresOperatorLifecycleRightsBeforeIntent(t *testing.T) {
	s, c := krrTestSession(t)
	journal := &krrTestJournal{}
	h := krrPrepared(t, s, journal)
	ssarAnswer(c, false, nil)
	_, e := s.ReviewKRR(t.Context(), krrTestOptions())
	require.Error(t, e)
	_, e = h.Execute(t.Context())
	require.Error(t, e)
	require.Empty(t, journal.record.ID)
	for _, a := range c.Actions() {
		if a.GetResource() == krrJobs {
			require.NotEqual(t, "create", a.GetVerb())
		}
	}
}

// Protocol fixtures reuse only the pinned engine sample shape. They do not
// execute the engine and explicitly adapt its subject namespace to the test API.
func krrProtocolReport(t *testing.T, namespace string) []byte {
	t.Helper()
	raw, e := os.ReadFile("../../krr/testdata/runner-simple.json")
	require.NoError(t, e)
	var data map[string]any
	require.NoError(t, json.Unmarshal(raw, &data))
	for _, scan := range data["scans"].([]any) {
		scan.(map[string]any)["object"].(map[string]any)["namespace"] = namespace
	}
	out, e := json.Marshal(data)
	require.NoError(t, e)
	return out
}

func TestKRRJobLateCreateCannotBeHiddenByImmediate404(t *testing.T) {
	s, c := krrTestSession(t)
	journal := &krrTestJournal{}
	h := krrPrepared(t, s, journal)
	creates := 0
	c.PrependReactor("create", "jobs", func(kt.Action) (bool, kruntime.Object, error) { creates++; return true, nil, io.ErrUnexpectedEOF })
	data, e := h.Execute(t.Context())
	require.ErrorIs(t, e, krr.ErrCleanup)
	require.Empty(t, data)
	require.Equal(t, 1, creates)
	require.True(t, journal.finished)
	require.False(t, journal.clean)
	require.Positive(t, journal.record.CreatedAt)
	// The API server commits the one original request after the client already
	// saw its first 404. Recovery observes and pins that UID before deleting it.
	late := h.g.job()
	late.SetUID("late-job")
	require.NoError(t, c.Tracker().Create(krrJobs, late, "tools"))
	pinned := ""
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	clean, e := s.cleanupKRRJob(ctx, journal.record, func(uid string) error { pinned = uid; return nil })
	require.NoError(t, e)
	require.True(t, clean)
	require.Equal(t, "late-job", pinned)
	require.Equal(t, 1, creates, "an ambiguous CREATE is never resent")
}
func TestKRRJobUnknownCreateAbsenceNeedsObservationHorizon(t *testing.T) {
	s, c := krrTestSession(t)
	h := krrPrepared(t, s, &krrTestJournal{})
	r := h.g.identity()
	r.Target = s.conn.target
	for _, created := range []int64{0, s.now().UnixMilli()} {
		r.CreatedAt = created
		clean, e := s.cleanupKRRJob(t.Context(), r, nil)
		require.ErrorIs(t, e, krr.ErrCleanup)
		require.False(t, clean)
	}
	r.CreatedAt = s.now().Add(-krrCreateAbsenceGrace - time.Second).UnixMilli()
	clean, e := s.cleanupKRRJob(t.Context(), r, nil)
	require.NoError(t, e)
	require.True(t, clean)
	for _, a := range c.Actions() {
		require.NotEqual(t, "delete", a.GetVerb())
	}
}
