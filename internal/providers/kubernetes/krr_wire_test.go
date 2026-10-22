package kubernetes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/krr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func TestKRRJobRealHTTPLifecycleAndNoAmbiguousCreateRetry(t *testing.T) {
	for _, mode := range []string{"success", "retry-after", "cancel", "oversize-log", "replaced-log-pod", "wrong-namespace", "changed-log-image"} {
		t.Run(mode, func(t *testing.T) {
			namespace := "tools"
			if mode == "wrong-namespace" {
				namespace = "other"
			}
			report := krrProtocolReport(t, namespace)
			var mu sync.Mutex
			var job, pod *unstructured.Unstructured
			creates, patches, deletes := 0, 0, 0
			started := make(chan struct{})
			var once sync.Once
			reply := func(w http.ResponseWriter, value any) {
				w.Header().Set("Content-Type", "application/json")
				if e := json.NewEncoder(w).Encode(value); e != nil && mode != "cancel" {
					require.NoError(t, e)
				}
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
					reply(w, map[string]any{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "status": map[string]any{"allowed": true}})
					return
				}
				jobPrefix := "/apis/batch/v1/namespaces/tools/jobs"
				podPrefix := "/api/v1/namespaces/tools/pods"
				if r.Method == "GET" && r.URL.Path == "/api/v1/namespaces/tools" {
					reply(w, map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "tools", "uid": "ns-uid"}})
					return
				}
				if r.Method == "GET" && r.URL.Path == "/api/v1/namespaces/tools/serviceaccounts/reader" {
					reply(w, map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"namespace": "tools", "name": "reader", "uid": "sa-uid"}})
					return
				}
				if r.Method == "POST" && r.URL.Path == jobPrefix {
					creates++
					require.Equal(t, "", r.URL.Query().Get("dryRun"))
					var object map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&object))
					// JSON numbers are normalized through the production client on subsequent GETs.
					job = &unstructured.Unstructured{Object: object}
					job.SetUID("wire-job")
					job.SetResourceVersion("1")
					suspended, _, _ := unstructured.NestedBool(job.Object, "spec", "suspend")
					require.True(t, suspended)
					require.NotEmpty(t, job.GetLabels()[krrJobLabel])
					require.Equal(t, krr.Image, str(slice(job.Object, "spec", "template", "spec", "containers")[0], "image"))
					if mode == "retry-after" {
						statusReply(w, 503, "ServiceUnavailable", true)
						return
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(201)
					reply(w, job)
					return
				}
				if r.Method == "PATCH" && job != nil && r.URL.Path == jobPrefix+"/"+job.GetName() {
					patches++
					require.Equal(t, "application/json-patch+json", r.Header.Get("Content-Type"))
					var patch []map[string]any
					require.NoError(t, json.NewDecoder(r.Body).Decode(&patch))
					require.Len(t, patch, 4)
					require.Equal(t, "/metadata/uid", patch[0]["path"])
					require.Equal(t, "wire-job", patch[0]["value"])
					_ = unstructured.SetNestedField(job.Object, false, "spec", "suspend")
					job.SetResourceVersion("2")
					spec, _, _ := unstructured.NestedMap(job.Object, "spec", "template", "spec")
					phase := "Succeeded"
					if mode == "cancel" {
						phase = "Running"
					}
					pod = &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": job.GetName() + "-pod", "namespace": "tools", "uid": "wire-pod", "labels": map[string]any{krrJobLabel: job.GetLabels()[krrJobLabel]}, "ownerReferences": []any{map[string]any{"apiVersion": "batch/v1", "kind": "Job", "name": job.GetName(), "uid": "wire-job", "controller": true}}}, "spec": spec, "status": map[string]any{"phase": phase, "containerStatuses": []any{map[string]any{"name": "krr", "state": map[string]any{"terminated": map[string]any{"exitCode": 0}}}}}}}
					once.Do(func() { close(started) })
					reply(w, job)
					return
				}
				if r.Method == "GET" && r.URL.Path == podPrefix {
					items := []any{}
					if pod != nil {
						items = append(items, pod.Object)
					}
					reply(w, map[string]any{"apiVersion": "v1", "kind": "PodList", "items": items})
					return
				}
				if r.Method == "GET" && pod != nil && r.URL.Path == podPrefix+"/"+pod.GetName()+"/log" {
					require.Equal(t, "true", r.URL.Query().Get("timestamps"))
					require.Equal(t, "krr", r.URL.Query().Get("container"))
					require.NotEmpty(t, r.URL.Query().Get("limitBytes"))
					if mode == "oversize-log" {
						_, _ = io.WriteString(w, strings.Repeat("x", krr.MaxBytes+256))
						return
					}
					_, _ = io.WriteString(w, "2026-10-22T00:00:00Z "+strings.TrimSpace(string(report))+"\n")
					if mode == "replaced-log-pod" {
						pod.SetUID("replacement")
					}
					if mode == "changed-log-image" {
						containers, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
						containers[0].(map[string]any)["image"] = "other:latest"
						_ = unstructured.SetNestedSlice(pod.Object, containers, "spec", "containers")
					}
					return
				}
				if r.Method == "GET" && pod != nil && r.URL.Path == podPrefix+"/"+pod.GetName() {
					reply(w, pod)
					return
				}
				if r.Method == "GET" && job != nil && r.URL.Path == jobPrefix+"/"+job.GetName() {
					reply(w, job)
					return
				}
				if r.Method == "DELETE" {
					var options metav1.DeleteOptions
					require.NoError(t, json.NewDecoder(r.Body).Decode(&options))
					require.NotNil(t, options.Preconditions)
					require.NotNil(t, options.Preconditions.UID)
					if job != nil && r.URL.Path == jobPrefix+"/"+job.GetName() {
						require.EqualValues(t, "wire-job", *options.Preconditions.UID)
						require.Equal(t, metav1.DeletePropagationForeground, *options.PropagationPolicy)
						job = nil
						deletes++
						reply(w, map[string]any{"kind": "Status", "status": "Success"})
						return
					}
					if pod != nil && r.URL.Path == podPrefix+"/"+pod.GetName() {
						require.Equal(t, pod.GetUID(), *options.Preconditions.UID)
						pod = nil
						deletes++
						reply(w, map[string]any{"kind": "Status", "status": "Success"})
						return
					}
				}
				statusReply(w, 404, "NotFound", false)
			}))
			defer server.Close()
			cfg := &rest.Config{Host: server.URL}
			dyn, e := dynamic.NewForConfig(cfg)
			require.NoError(t, e)
			s := newSession("fixture", "hash", dyn, false)
			defer s.Close()
			s.conn = newConn(cfg, dyn, "fixture", "fixture", "hash")
			s.writer, e = newRESTWriter(cfg)
			require.NoError(t, e)
			s.logs, e = httpLogFetcher(cfg)
			require.NoError(t, e)
			journal := &krrTestJournal{}
			h := krrPrepared(t, s, journal)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if mode == "cancel" {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			data, e := h.Execute(ctx)
			if mode == "success" {
				require.NoError(t, e)
				require.NotEmpty(t, data)
			} else {
				require.Error(t, e)
				require.Empty(t, data)
			}
			require.True(t, journal.clean)
			require.True(t, journal.finished)
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, 1, creates)
			require.Nil(t, job)
			require.Nil(t, pod)
			if mode == "retry-after" {
				require.Zero(t, patches)
				require.Equal(t, 1, deletes)
			} else {
				require.Equal(t, 1, patches)
				require.Equal(t, 2, deletes)
			}
		})
	}
}
