package kubernetes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	kt "k8s.io/client-go/testing"
)

const historyUID = "12345678-abcd-1234-abcd-123456789012"

func historyPod() *unstructured.Unstructured {
	u := mk("v1", "Pod", "blue", "web", historyUID, map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app", "env": []any{map[string]any{"value": "PRIVATE-ENV"}}, "resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "64Mi"}}}}}})
	u.SetResourceVersion("7")
	u.SetCreationTimestamp(metav1.NewTime(time.Unix(1800, 0)))
	return u
}
func podHistoryRequest(url string) core.HistoryRequest {
	return core.HistoryRequest{Ref: core.Ref{Provider: ProviderID, Target: "t", Kind: "pods", Scope: "blue", Name: "web", UID: historyUID}, Source: core.HistorySource{URL: url, Dedicated: true, Token: "PRIVATE-TOKEN"}, Window: "1h"}
}
func TestPodHistoryFencesRevisionUIDAndExcludesPrivateDeclarations(t *testing.T) {
	for _, mode := range []string{"current", "uid", "revision", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			pod := historyPod()
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), pod)
			s := newSession("t", "h", client, false)
			defer s.Close()
			s.now = func() time.Time { return time.Unix(3600, 0) }
			reads := 0
			client.PrependReactor("get", "pods", func(_ kt.Action) (bool, runtime.Object, error) {
				reads++
				u := pod.DeepCopy()
				if reads == 2 {
					if mode == "uid" {
						u.SetUID("replacement")
					}
					if mode == "revision" {
						u.SetResourceVersion("8")
					}
				}
				return true, u, nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queries := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				queries++
				if mode == "cancel" {
					cancel()
				}
				require.Equal(t, "GET", r.Method)
				require.Contains(t, r.URL.Query().Get("query"), historyUID)
				require.Equal(t, "1800", r.URL.Query().Get("start"))
				_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[]}}`))
			}))
			defer server.Close()
			out, e := s.ResourceMetricsHistory(ctx, podHistoryRequest(server.URL))
			if mode != "current" {
				require.Error(t, e)
				return
			}
			require.NoError(t, e)
			require.Equal(t, 2, reads)
			require.Equal(t, 3, queries)
			require.Equal(t, int64(1800), out.Start)
			require.Equal(t, int64(0), out.RequestedStart)
			require.Equal(t, int64(60), out.Step)
			require.Len(t, out.Declarations, 1)
			require.Equal(t, .1, *out.Declarations[0].CPURequest)
			require.Nil(t, out.Declarations[0].CPULimit)
			b, e := json.Marshal(out)
			require.NoError(t, e)
			require.NotContains(t, string(b), "PRIVATE")
			require.NotContains(t, string(b), "env")
		})
	}
}
func TestPodHistoryRejectsUnsupportedAndOversizedBeforeExternalReads(t *testing.T) {
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), historyPod())
	s := newSession("t", "h", client, false)
	defer s.Close()
	for _, kind := range []string{"apps/deployments", "nodes", ""} {
		r := podHistoryRequest("https://unreachable.invalid")
		r.Ref.Kind = kind
		_, e := s.ResourceMetricsHistory(t.Context(), r)
		require.Error(t, e)
	}
	require.Empty(t, client.Actions())
	p := historyPod()
	_ = unstructured.SetNestedField(p.Object, strings.Repeat("a", 1<<20), "spec", "private")
	client.PrependReactor("get", "pods", func(kt.Action) (bool, runtime.Object, error) { return true, p, nil })
	_, e := s.ResourceMetricsHistory(t.Context(), podHistoryRequest("https://unreachable.invalid"))
	require.ErrorContains(t, e, "exceeds")
}
