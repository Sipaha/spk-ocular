package metricshistory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

const fixtureUID = "12345678-abcd-1234-abcd-123456789012"

func historyRequest(url string) core.HistoryRequest {
	return core.HistoryRequest{Ref: core.Ref{Provider: "kubernetes", Target: "t", Kind: "pods", Scope: "blue", Name: "web", UID: fixtureUID}, Source: core.HistorySource{URL: url, ClusterLabel: "cluster", ClusterValue: "alpha", Token: "PRIVATE-TOKEN"}, Window: "1h"}
}
func row(id string, values any) map[string]any {
	return map[string]any{"metric": map[string]string{"namespace": "blue", "pod": "web", "container": "app", "id": id, "cluster": "alpha"}, "values": values}
}
func matrix(rows ...map[string]any) map[string]any {
	return map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": rows}}
}
func TestReadScopesEachMetricAndPreservesGapsAndZero(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "GET", r.Method)
		require.Equal(t, "/prefix/api/v1/query_range", r.URL.Path)
		require.Equal(t, "Bearer PRIVATE-TOKEN", r.Header.Get("Authorization"))
		q := r.URL.Query()
		require.Equal(t, "60", q.Get("step"))
		require.Equal(t, "33", q.Get("limit"))
		require.Contains(t, q.Get("query"), `cluster="alpha"`)
		require.Contains(t, q.Get("query"), `namespace="blue",pod="web"`)
		require.Contains(t, q.Get("query"), fixtureUID)
		require.NoError(t, json.NewEncoder(w).Encode(matrix(row("/kubepods/burstable/pod"+fixtureUID+"/runtime", [][]any{{float64(120), "0"}, {float64(240), "0.5"}}))))
	}))
	defer server.Close()
	got, e := Read(t.Context(), historyRequest(server.URL+"/prefix"), 120, 240, []string{"app"})
	require.NoError(t, e)
	require.Equal(t, 3, calls)
	for _, m := range got {
		require.Equal(t, "available", m.State)
		require.Len(t, m.Series, 1)
		require.Equal(t, 0.0, *m.Series[0].Values[0])
		require.Nil(t, m.Series[0].Values[1])
		require.Equal(t, .5, *m.Series[0].Values[2])
		b, _ := json.Marshal(m)
		require.NotContains(t, string(b), "PRIVATE")
		require.NotContains(t, string(b), "kubepods")
	}
}
func TestIdentityGrammar(t *testing.T) {
	for _, c := range []struct {
		id    string
		valid bool
	}{
		{"/kubepods/pod" + fixtureUID + "/abc", true},
		{"/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod" + strings.ReplaceAll(fixtureUID, "-", "_") + ".slice/cri-containerd-abc.scope", true},
		{"/kubepods/pod" + fixtureUID + "x/abc", false}, {"/kubepods/notpod" + fixtureUID + "/abc", false}, {"/kubepods/pod" + fixtureUID, false}, {"/kubepods/pod" + fixtureUID + "/abc/child", false}, {"/kubepods/podold/abc", false},
	} {
		t.Run(c.id, func(t *testing.T) {
			req := historyRequest("https://example.invalid")
			b, _ := json.Marshal(matrix(row(c.id, [][]any{{float64(120), "1"}})))
			rows, state, _, _ := project(b, req, compileIdentity(), map[string]bool{"app": true}, 120, 120, 32, 50000, "cpu")
			require.Equal(t, c.valid, state == "available")
			if c.valid {
				require.Len(t, rows, 1)
			}
		})
	}
}
func TestUntrustedMatricesNeverBecomeZeroOrMixedHistory(t *testing.T) {
	cases := map[string]func() map[string]any{
		"foreign cluster": func() map[string]any {
			r := row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "1"}})
			r["metric"].(map[string]string)["cluster"] = "other"
			return matrix(r)
		},
		"foreign namespace": func() map[string]any {
			r := row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "1"}})
			r["metric"].(map[string]string)["namespace"] = "other"
			return matrix(r)
		},
		"old uid":    func() map[string]any { return matrix(row("/podold/r", [][]any{{float64(120), "1"}})) },
		"missing id": func() map[string]any { return matrix(row("", [][]any{{float64(120), "1"}})) },
		"duplicate scrape": func() map[string]any {
			r := row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "1"}})
			return matrix(r, r)
		},
		"overlapping incarnations": func() map[string]any {
			return matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "1"}}), row("/pod"+fixtureUID+"/s", [][]any{{float64(120), "2"}}))
		},
		"outside window": func() map[string]any { return matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(60), "1"}})) },
		"off grid":       func() map[string]any { return matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(121), "1"}})) },
		"nonfinite":      func() map[string]any { return matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "NaN"}})) },
		"negative":       func() map[string]any { return matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "-1"}})) },
		"warnings":       func() map[string]any { m := matrix(); m["warnings"] = []string{"PRIVATE-UPSTREAM"}; return m },
		"wrong type":     func() map[string]any { m := matrix(); m["data"].(map[string]any)["resultType"] = "vector"; return m },
	}
	for n, f := range cases {
		t.Run(n, func(t *testing.T) {
			b, _ := json.Marshal(f())
			rows, state, _, _ := project(b, historyRequest("https://example.invalid"), compileIdentity(), map[string]bool{"app": true}, 120, 240, 32, 50000, "cpu")
			require.Equal(t, "unavailable", state)
			require.Empty(t, rows)
		})
	}
}
func TestLimitsRejectWholeMetric(t *testing.T) {
	b, _ := json.Marshal(matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "1"}, {float64(180), "2"}})))
	for _, limits := range [][2]int{{0, 50000}, {32, 1}} {
		rows, state, _, _ := project(b, historyRequest("https://example.invalid"), compileIdentity(), map[string]bool{"app": true}, 120, 240, limits[0], limits[1], "cpu")
		require.Empty(t, rows)
		require.Equal(t, "limited", state)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(4<<20+1))
		w.WriteHeader(200)
	}))
	defer server.Close()
	got, e := Read(t.Context(), historyRequest(server.URL), 120, 240, []string{"app"})
	require.NoError(t, e)
	for _, m := range got {
		require.Equal(t, "limited", m.State)
		require.Empty(t, m.Series)
	}
}
func TestNoRedirectCredentialForwardingAndNoUpstreamErrorLeak(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
	defer destination.Close()
	for _, status := range []int{302, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", destination.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("PRIVATE-UPSTREAM"))
			}))
			defer server.Close()
			got, e := Read(t.Context(), historyRequest(server.URL), 120, 240, []string{"app"})
			require.NoError(t, e)
			b, _ := json.Marshal(got)
			require.NotContains(t, string(b), "PRIVATE")
			for _, m := range got {
				require.Empty(t, m.Series)
			}
			require.Zero(t, forwarded.Load())
		})
	}
}
func TestCancellationStopsOwnedQuery(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done(); close(ended) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { _, e := Read(ctx, historyRequest(server.URL), 120, 240, []string{"app"}); result <- e }()
	<-started
	cancel()
	select {
	case e := <-result:
		require.ErrorIs(t, e, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("query did not cancel")
	}
	<-ended
}
func TestExplicitSourceValidation(t *testing.T) {
	for _, url := range []string{"http://example.com", "https://user:PRIVATE@example.com", "https://example.com?token=PRIVATE", "https://example.com/#fragment", "https://example.com/a/../b"} {
		r := historyRequest(url)
		require.Error(t, Validate(r.Source, r.Window))
	}
	r := historyRequest("https://example.com")
	require.NoError(t, Validate(r.Source, r.Window))
	r.Source.ClusterLabel = "pod"
	require.Error(t, Validate(r.Source, r.Window))
	r.Source.ClusterLabel = "cluster"
	r.Source.Token = "PRIVATE\nheader"
	require.Error(t, Validate(r.Source, r.Window))
	r.Source.Token = ""
	r.Source.Dedicated = true
	require.Error(t, Validate(r.Source, r.Window))
	r.Source.ClusterLabel = ""
	r.Source.ClusterValue = ""
	require.NoError(t, Validate(r.Source, r.Window))
}

func compileIdentity() *regexp.Regexp { return regexp.MustCompile(identityPattern(fixtureUID)) }

func TestMissingContainerCoverageIsExplicit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(matrix(row("/pod"+fixtureUID+"/r", [][]any{{float64(120), "0"}}))))
	}))
	defer server.Close()
	got, e := Read(t.Context(), historyRequest(server.URL), 120, 120, []string{"app", "missing"})
	require.NoError(t, e)
	for _, m := range got {
		require.Equal(t, "partial", m.State)
		require.Equal(t, []string{"missing"}, m.MissingContainers)
		require.Equal(t, 0.0, *m.Series[0].Values[0])
	}
}
