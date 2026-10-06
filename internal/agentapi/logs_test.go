package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/agentgrant"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

func logTime(second int) time.Time { return time.Date(2026, 1, 1, 0, 0, second, 0, time.UTC) }
func logLine(second int, text string) provider.LogLine {
	return provider.LogLine{TS: logTime(second).Format(time.RFC3339Nano), Text: text}
}

func TestAgentLogIntervalReadsHistoryBeforeApplyingTail(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	var query provider.LogQuery
	e.f.logs = func(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		query = q
		require.NoError(t, sink.Source(1, "one", "first", "main"))
		require.NoError(t, sink.Source(2, "two", "second", "sidecar"))
		require.NoError(t, sink.Lines(1, []provider.LogLine{logLine(0, "old"), logLine(1, "start"), logLine(2, "first-middle"), logLine(5, "end"), logLine(8, "new")}))
		// Later delivery from another source must not be discarded merely
		// because the first source already passed the interval's end.
		require.NoError(t, sink.Lines(2, []provider.LogLine{logLine(1, "second-start"), logLine(3, "second-middle")}))
		return nil
	}
	request := GetLogsRequest{Ref: ref("a", "pods", "web-1"), Channel: "*", SinceTime: logTime(1), UntilTime: logTime(5), Limit: 50}
	out := e.ok("GetLogs", request)
	assert.Len(t, out["lines"], 4)
	assert.NotContains(t, out, "truncated")
	assert.Equal(t, provider.TailAll, query.TailLines)
	assert.Equal(t, "*", query.Channel)
	assert.False(t, query.Follow)
	assert.Equal(t, logTime(1), query.SinceTime)
	request.TailLines = 1
	out = e.ok("GetLogs", request)
	lines := out["lines"].([]any)
	require.Len(t, lines, 2)
	assert.Equal(t, "first-middle", lines[0].(map[string]any)["text"])
	assert.Equal(t, "second-middle", lines[1].(map[string]any)["text"])
	assert.Equal(t, true, out["truncated"])
	assert.Equal(t, provider.TailAll, query.TailLines, "an upper bound must not take today's tail first")
	request.Limit = 1
	out = e.ok("GetLogs", request)
	require.Len(t, out["lines"], 1)
	assert.Equal(t, true, out["truncated"])
}

func TestAgentLogsReportUnstampedLinesAndProviderGaps(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		if err := sink.Source(1, "one", "first", "main"); err != nil {
			return err
		}
		if err := sink.Lines(1, []provider.LogLine{{Text: "no timestamp"}, logLine(3, "included")}); err != nil {
			return err
		}
		return sink.State(1, provider.LogState{State: provider.LogTruncated, Message: "provider byte limit"})
	}
	out := e.ok("GetLogs", GetLogsRequest{Ref: ref("a", "pods", "web-1"), SinceTime: logTime(1), Limit: 10})
	require.Len(t, out["lines"], 1)
	assert.Equal(t, true, out["truncated"])
	assert.Contains(t, out["sources"].([]any)[0].(map[string]any)["state"], "state")
}

func TestLogInfoNeedsOnlyLogsAndHonorsNamespaceAndKind(t *testing.T) {
	e := newEnv(t)
	request := GetLogInfoRequest{Ref: ref("a", "pods", "web-1")}
	e.grant(one("a", agentgrant.VerbRead))
	e.refused("GetLogInfo", request)
	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	assert.Equal(t, "main", e.ok("GetLogInfo", request)["defaultChannel"])
	e.refused("GetLogInfo", GetLogInfoRequest{Ref: ref("b", "pods", "api-1")})
	e.refused("GetLogs", GetLogsRequest{Ref: ref("b", "pods", "api-1"), TailLines: -1})
	for _, request := range []GetLogsRequest{
		{Ref: ref("a", "pods", "web-1"), SinceTime: logTime(5), UntilTime: logTime(1)},
		{Ref: ref("a", "pods", "web-1"), SinceTime: logTime(5), UntilTime: logTime(5), Limit: 50},
		{Ref: ref("a", "pods", "web-1"), TailLines: -2},
		{Ref: ref("a", "pods", "web-1"), Limit: 5001},
	} {
		code, _ := e.call("GetLogs", request)
		assert.Equal(t, http.StatusBadRequest, code)
	}
}

func streamRequest(t *testing.T, e *env, request StreamLogsRequest) *http.Response {
	t.Helper()
	server := httptest.NewServer(e.srv.Handler())
	t.Cleanup(server.Close)
	body, err := json.Marshal(request)
	require.NoError(t, err)
	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/v1/StreamLogs", bytes.NewReader(body))
	require.NoError(t, err)
	response, err := server.Client().Do(r)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func TestStreamLogsProgressiveHistoryAndDeniedScope(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs, "pods"))
	request := StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("b", "pods", "api-1"), TailLines: -1, Limit: 12, MaxBytes: 1 << 20}}
	response := streamRequest(t, e, request)
	assert.Equal(t, http.StatusForbidden, response.StatusCode)
	require.NoError(t, response.Body.Close())
	request.Ref = ref("a", "pods", "web-1")
	e.f.logs = func(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		assert.Equal(t, -1, q.TailLines)
		if err := sink.Source(1, "stable-key", "pod/container", "main"); err != nil {
			return err
		}
		for i := range 6000 {
			if err := sink.Lines(1, []provider.LogLine{logLine(2, string(rune('a'+i%26)))}); err != nil {
				return err
			}
		}
		return sink.Ready()
	}
	response = streamRequest(t, e, request)
	require.Equal(t, http.StatusOK, response.StatusCode)
	assert.Equal(t, "application/x-ndjson", response.Header.Get("Content-Type"))
	decoder := json.NewDecoder(response.Body)
	count, ended := 0, false
	for {
		var frame LogFrame
		err := decoder.Decode(&frame)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		count += len(frame.Lines)
		if frame.Type == "end" {
			ended = true
			assert.False(t, frame.Complete)
			assert.True(t, frame.Truncated)
			assert.Equal(t, "line_limit", frame.StopReason)
		}
	}
	assert.Equal(t, 12, count, "the requested line budget bounds all returned log data")
	assert.True(t, ended)
}

func TestQuietLogStreamStopsOnGrantChangeAndReleasesItsSlot(t *testing.T) {
	for _, operation := range []string{"revoke target", "revoke all", "pause scope", "pause group", "client disconnect", "shutdown"} {
		t.Run(operation, func(t *testing.T) {
			e := newEnv(t)
			e.grant(one("a", agentgrant.VerbLogs))
			started, stopped := make(chan struct{}), make(chan struct{})
			e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
				defer close(stopped)
				if err := sink.Source(1, "key", "quiet", "main"); err != nil {
					return err
				}
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}
			response := streamRequest(t, e, StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 10}, Follow: true})
			require.Equal(t, http.StatusOK, response.StatusCode)
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("provider did not start")
			}
			switch operation {
			case "revoke target":
				e.grant()
			case "revoke all":
				require.NoError(t, e.srv.RevokeAll(t.Context()))
			case "pause scope", "pause group":
				scope := agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: "a"}
				req := api.SaveAgentGrantsRequest{Provider: "k", Target: "t", Groups: []agentgrant.Group{{ID: "logs", Name: "Logs", Scope: scope, Grants: []agentgrant.Grant{one("a", agentgrant.VerbLogs)}}}}
				if operation == "pause scope" {
					req.DisabledScopes = []agentgrant.Scope{scope}
				} else {
					req.Groups[0].Disabled = true
				}
				require.NoError(t, e.svc.SaveAgentGrants(t.Context(), req))
			case "client disconnect":
				require.NoError(t, response.Body.Close())
			case "shutdown":
				e.srv.Close()
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("quiet stream survived its access/lifecycle change")
			}
			if operation == "revoke target" || operation == "revoke all" || operation == "pause scope" || operation == "pause group" {
				body, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), `"code":"forbidden"`)
				assert.Contains(t, string(body), `"type":"end"`)
			}
			require.Eventually(t, func() bool { e.srv.logMu.Lock(); defer e.srv.logMu.Unlock(); return len(e.srv.logReads) == 0 }, time.Second, time.Millisecond)
		})
	}
}

func TestAgentLogReadConcurrencyIsBounded(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, _ provider.LogSink) error {
		<-ctx.Done()
		return ctx.Err()
	}
	request := StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 10}, Follow: true}
	for range maxLogReads {
		require.Equal(t, http.StatusOK, streamRequest(t, e, request).StatusCode)
	}
	assert.Equal(t, http.StatusTooManyRequests, streamRequest(t, e, request).StatusCode)
}

func TestPreviousContainerLogsSupportTimeIntervals(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		assert.True(t, q.Previous)
		assert.True(t, q.SinceTime.IsZero(), "previous history is filtered locally")
		assert.Equal(t, -1, q.TailLines)
		if err := sink.Source(1, "old", "previous", "main"); err != nil {
			return err
		}
		return sink.Lines(1, []provider.LogLine{logLine(0, "old"), logLine(1, "wanted"), logLine(2, "newer")})
	}
	out := e.ok("GetLogs", GetLogsRequest{Ref: ref("a", "pods", "web-1"), Previous: true, SinceTime: logTime(1), UntilTime: logTime(2), Limit: 10})
	lines := out["lines"].([]any)
	require.Len(t, lines, 1)
	assert.Equal(t, "wanted", lines[0].(map[string]any)["text"])
}

func TestFollowStopsAtUpperTimeBoundAndReportsCompletion(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(ctx context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		assert.True(t, q.Follow)
		if err := sink.Source(1, "quiet", "quiet", "main"); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	}
	response := streamRequest(t, e, StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1"), UntilTime: time.Now().Add(300 * time.Millisecond), Limit: 10}, Follow: true})
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"type":"end","complete":true`)
	assert.NotContains(t, string(body), `"type":"error"`)
}

func TestRevocationDiscardsAnInFlightSnapshot(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	started := make(chan struct{})
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		close(started)
		<-ctx.Done()
		// Even a slow provider delivering after cancellation cannot return
		// its buffered data in the now-revoked JSON response.
		return sink.Lines(1, []provider.LogLine{{Text: "must not escape"}})
	}
	body, err := json.Marshal(GetLogsRequest{Ref: ref("a", "pods", "web-1")})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/GetLogs", bytes.NewReader(body)))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	e.grant()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("snapshot did not end")
	}
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "must not escape")
}

func TestLogGrepFiltersBeforeTheResponseLimit(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		assert.True(t, q.Archive, "search scans retained history, not an implicit recent tail")
		if err := sink.Source(1, "key", "pod/main", "main"); err != nil {
			return err
		}
		return sink.Lines(1, []provider.LogLine{logLine(1, "noise"), logLine(2, "[Error] code=10"), logLine(3, "noise"), logLine(4, "[error] code=20"), logLine(5, "noise")})
	}
	for _, grep := range []*api.LogGrep{
		{Pattern: "[error]", IgnoreCase: true},
		{Pattern: `\[error\] code=\d+`, Regex: true, IgnoreCase: true},
		{Pattern: "noise", Invert: true},
	} {
		out := e.ok("GetLogs", GetLogsRequest{Ref: ref("a", "pods", "web-1"), Grep: grep, Limit: 2})
		lines := out["lines"].([]any)
		require.Len(t, lines, 2)
		assert.Contains(t, lines[0].(map[string]any)["text"], "code=10")
		assert.Contains(t, lines[1].(map[string]any)["text"], "code=20")
	}
	for _, request := range []GetLogsRequest{
		{Ref: ref("a", "pods", "web-1"), SinceTime: logTime(1)},
		{Ref: ref("a", "pods", "web-1"), UntilTime: logTime(2)},
		{Ref: ref("a", "pods", "web-1"), Grep: &api.LogGrep{Pattern: "[", Regex: true}},
		{Ref: ref("a", "pods", "web-1"), Grep: &api.LogGrep{Pattern: strings.Repeat("x", 4097)}},
	} {
		status, _ := e.call("GetLogs", request)
		assert.Equal(t, http.StatusBadRequest, status)
	}
}

func TestLogResponsesHaveAnEncodedByteBudget(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		if err := sink.Source(1, "key", "pod/main", "main"); err != nil {
			return err
		}
		lines := make([]provider.LogLine, 100)
		for i := range lines {
			lines[i] = logLine(2, strings.Repeat("<", 100))
		}
		return sink.Lines(1, lines)
	}
	query := GetLogsRequest{Ref: ref("a", "pods", "web-1"), Limit: 100, MaxBytes: 4096}
	data, err := json.Marshal(query)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	e.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/GetLogs", bytes.NewReader(data)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.LessOrEqual(t, w.Body.Len(), 4096, "JSON escaping and metadata also count")
	assert.Contains(t, w.Body.String(), `"byte_limit"`)
	response := streamRequest(t, e, StreamLogsRequest{GetLogsRequest: query})
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(body), 4096)
	assert.Contains(t, string(body), `"type":"lines"`, "a smaller prefix of a large provider batch still fits")
	assert.Contains(t, string(body), `"stopReason":"byte_limit"`)
	assert.Contains(t, string(body), `"type":"end"`)
}

func TestStreamRequiresALimitAndHasATimeBudget(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	query := StreamLogsRequest{GetLogsRequest: GetLogsRequest{Ref: ref("a", "pods", "web-1")}, Follow: true}
	assert.Equal(t, http.StatusBadRequest, streamRequest(t, e, query).StatusCode)
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, _ provider.LogSink) error {
		<-ctx.Done()
		return ctx.Err()
	}
	query.Limit, query.MaxSeconds = 3, 1
	response := streamRequest(t, e, query)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"stopReason":"time_limit"`)
	assert.Contains(t, string(body), `"type":"end"`)
}
