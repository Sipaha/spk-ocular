package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	ktesting "k8s.io/client-go/testing"
)

func diagnosisRef() core.Ref {
	return core.Ref{Provider: ProviderID, Target: "t", Kind: "pods", Scope: "blue", Name: "p", UID: "p-uid"}
}
func diagnosisPod() *unstructured.Unstructured {
	return mk("v1", "Pod", "blue", "p", "p-uid", map[string]any{
		"spec":   map[string]any{"containers": []any{map[string]any{"name": "app", "env": []any{map[string]any{"name": "plain", "value": "PRIVATE-ENV"}}}}},
		"status": map[string]any{"phase": "Running", "containerStatuses": []any{map[string]any{"name": "app", "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff", "message": "backoff"}}, "lastState": map[string]any{"terminated": map[string]any{"exitCode": int64(137), "reason": "Error", "finishedAt": "2026-10-21T10:00:00Z"}}}}},
	})
}
func TestDiagnosisUIDEventsAndContainerEvidence(t *testing.T) {
	p := diagnosisPod()
	event := func(uid, subjectUID string) *unstructured.Unstructured {
		return mk("v1", "Event", "blue", uid, uid, map[string]any{"involvedObject": map[string]any{"uid": subjectUID, "name": "p", "namespace": "blue"}, "type": "Warning", "reason": "Unhealthy", "message": "Readiness probe failed", "lastTimestamp": "2026-10-21T10:00:00Z"})
	}
	client := fullFake(p, event("current", "p-uid"), event("replacement", "different"))
	s := newSession("t", "h", client, false)
	defer s.Close()
	out, err := s.DiagnoseResource(t.Context(), diagnosisRef())
	require.NoError(t, err)
	b, _ := json.Marshal(out)
	text := string(b)
	require.Contains(t, text, "CrashLoopBackOff")
	require.Contains(t, text, "exitCode=137")
	require.NotContains(t, text, "OOMKilled")
	require.NotContains(t, text, "PRIVATE-ENV")
	require.NotContains(t, text, "replacement")
	require.Contains(t, text, "Readiness probe failed")
	for _, a := range client.Actions() {
		require.Equal(t, "blue", a.GetNamespace())
		if a.GetVerb() == "list" {
			require.Equal(t, "events", a.GetResource().Resource)
			require.Equal(t, "involvedObject.uid=p-uid", a.(ktesting.ListAction).GetListRestrictions().Fields.String())
		}
	}
	p.Object["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)["lastState"].(map[string]any)["terminated"].(map[string]any)["reason"] = "OOMKilled"
	_, err = client.Resource(podsKind.gvr).Namespace("blue").Update(t.Context(), p, metav1.UpdateOptions{})
	require.NoError(t, err)
	out, err = s.DiagnoseResource(t.Context(), diagnosisRef())
	require.NoError(t, err)
	b, _ = json.Marshal(out)
	require.Contains(t, string(b), "OOMKilled")
}
func TestDiagnosisReferencesMetadataOnlyAndUnknownCoverage(t *testing.T) {
	p := diagnosisPod()
	containers := slice(p.Object, "spec", "containers")
	containers[0]["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "denied"}}, map[string]any{"configMapRef": map[string]any{"name": "missing"}}, map[string]any{"secretRef": map[string]any{"name": "optional", "optional": true}}}
	containers[0]["env"] = []any{map[string]any{"name": "key", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "exists", "key": "PRIVATE-KEY"}}}}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		require.Contains(t, r.Header.Get("Accept"), "PartialObjectMetadata")
		require.Equal(t, http.MethodGet, r.Method)
		require.True(t, strings.HasPrefix(r.URL.Path, "/api/v1/namespaces/blue/"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/denied"):
			w.WriteHeader(403)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","message":"PRIVATE-SERVER","code":403}`)
		case strings.HasSuffix(r.URL.Path, "/exists"):
			_, _ = fmt.Fprint(w, `{"apiVersion":"meta.k8s.io/v1","kind":"PartialObjectMetadata","metadata":{"name":"exists","namespace":"blue","uid":"secret-uid"}}`)
		default:
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
	defer server.Close()
	client := fullFake(p)
	s := newSession("t", "h", client, false)
	defer s.Close()
	var err error
	s.graphMetadata, err = metadata.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	out, err := s.DiagnoseResource(t.Context(), diagnosisRef())
	require.NoError(t, err)
	require.EqualValues(t, 4, reads.Load())
	b, _ := json.Marshal(out)
	text := string(b)
	require.Contains(t, text, "keys_unchecked")
	require.Contains(t, text, "forbidden")
	require.NotContains(t, text, "PRIVATE-")
	missing := 0
	for _, e := range out.Evidence {
		if e.Rule != "reference" {
			continue
		}
		switch e.Reason {
		case "RequiredReferenceMissing":
			missing++
			require.Equal(t, "configmaps/missing", e.Message)
			require.Equal(t, core.HealthWarning, e.State)
		case "OptionalReferenceAbsent":
			require.Equal(t, core.HealthOK, e.State)
		}
	}
	require.Equal(t, 1, missing)
	for _, a := range client.Actions() {
		require.NotContains(t, []string{"secrets", "configmaps"}, a.GetResource().Resource)
	}
	// No metadata client is unknown, never missing.
	s.graphMetadata = nil
	out, err = s.DiagnoseResource(t.Context(), diagnosisRef())
	require.NoError(t, err)
	for _, e := range out.Evidence {
		require.NotEqual(t, "RequiredReferenceMissing", e.Reason)
	}
}
func TestDiagnosisReplacementCancellationAndUnsupported(t *testing.T) {
	client := fullFake(diagnosisPod())
	s := newSession("t", "h", client, false)
	defer s.Close()
	count := 0
	client.PrependReactor("get", "pods", func(ktesting.Action) (bool, runtime.Object, error) {
		count++
		if count == 2 {
			p := diagnosisPod()
			p.SetUID("replacement")
			return true, p, nil
		}
		return false, nil, nil
	})
	out, err := s.DiagnoseResource(t.Context(), diagnosisRef())
	require.Error(t, err)
	require.Empty(t, out.Evidence)
	var pe *provider.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, provider.ClassGone, pe.Class)
	before := len(client.Actions())
	ref := diagnosisRef()
	ref.Kind = "secrets"
	_, err = s.DiagnoseResource(t.Context(), ref)
	require.Error(t, err)
	require.Len(t, client.Actions(), before)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = s.DiagnoseResource(ctx, diagnosisRef())
	require.ErrorIs(t, err, context.Canceled)
	s.Close()
	_, err = s.DiagnoseResource(t.Context(), diagnosisRef())
	require.ErrorIs(t, err, context.Canceled)
}
func TestDiagnosisJobOwnershipAndListBounds(t *testing.T) {
	job := mk("batch/v1", "Job", "blue", "job", "job-uid", nil)
	owned := diagnosisPod()
	owned.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: "job", UID: "job-uid", Controller: ptrTrue()}})
	wrong := diagnosisPod()
	wrong.SetName("wrong")
	wrong.SetUID("wrong")
	wrong.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: "job", UID: "other", Controller: ptrTrue()}})
	client := fullFake(job, owned, wrong)
	s := newSession("t", "h", client, false)
	defer s.Close()
	def := diagnosisSource("batch/jobs", "Jobs", "Job", "batch", "v1", "jobs").def
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(append(append([]*kindDef{}, allKinds.list...), def)...), state: catalogReady})
	ref := core.Ref{Provider: ProviderID, Target: "t", Kind: "batch/jobs", Scope: "blue", Name: "job", UID: "job-uid"}
	out, err := s.DiagnoseResource(t.Context(), ref)
	require.NoError(t, err)
	n := 0
	for _, e := range out.Evidence {
		if e.Rule == "owned_pod" {
			n++
			require.Equal(t, "p-uid", e.Related.UID)
		}
	}
	require.Equal(t, 1, n)
	client.PrependReactor("list", "events", func(ktesting.Action) (bool, runtime.Object, error) {
		list := &unstructured.UnstructuredList{}
		list.SetContinue("repeat")
		return true, list, nil
	})
	out, err = s.DiagnoseResource(t.Context(), ref)
	require.NoError(t, err)
	require.True(t, out.Truncated)
	require.Contains(t, out.Coverage, core.DiagnosisCoverage{Source: ref.String() + "/events", State: "partial"})
}
func ptrTrue() *bool { v := true; return &v }

func TestDiagnosisJobTemplateReferencesBeforePodsAndDependencyBudget(t *testing.T) {
	refs := []any{}
	for i := 0; i < 40; i++ {
		refs = append(refs, map[string]any{"configMapRef": map[string]any{"name": fmt.Sprintf("missing-%d", i)}})
	}
	job := mk("batch/v1", "Job", "blue", "job", "job-uid", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "envFrom": refs}}}}}})
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		require.Contains(t, r.Header.Get("Accept"), "PartialObjectMetadata")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
	}))
	defer server.Close()
	client := fullFake(job)
	s := newSession("t", "h", client, false)
	defer s.Close()
	def := diagnosisSource("batch/jobs", "Jobs", "Job", "batch", "v1", "jobs").def
	s.cat.cur.Store(&catalogSnap{reg: newKindRegistry(append(append([]*kindDef{}, allKinds.list...), def)...), state: catalogReady})
	var err error
	s.graphMetadata, err = metadata.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	ref := core.Ref{Provider: ProviderID, Target: "t", Scope: "blue", Kind: "batch/jobs", Name: "job", UID: "job-uid"}
	out, err := s.DiagnoseResource(t.Context(), ref)
	require.NoError(t, err)
	require.EqualValues(t, 32, reads.Load())
	require.True(t, out.Truncated)
	missing := 0
	for _, e := range out.Evidence {
		if e.Reason == "RequiredReferenceMissing" {
			missing++
			require.Equal(t, ref, e.Ref)
			require.True(t, strings.HasPrefix(e.Field, "spec.template.spec.containers[0].envFrom["))
		}
	}
	require.Equal(t, 32, missing)
	require.Contains(t, out.Coverage, core.DiagnosisCoverage{Source: ref.String() + "/references", State: "partial"})
}
