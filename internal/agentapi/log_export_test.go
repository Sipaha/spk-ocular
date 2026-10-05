package agentapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestExportLogsWritesPrivateFilesAndReturnsOnlyMetadata(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, q provider.LogQuery, sink provider.LogSink) error {
		assert.True(t, q.Archive)
		assert.False(t, q.Follow)
		if err := sink.Source(1, "key", "web/main", "main"); err != nil {
			return err
		}
		for i := range 6001 {
			if err := sink.Lines(1, []provider.LogLine{logLine(1, fmt.Sprintf("export-only-line-%d", i))}); err != nil {
				return err
			}
		}
		return nil
	}
	var previous string
	for _, format := range []string{"text", "ndjson"} {
		out := e.ok("ExportLogs", ExportLogsRequest{Ref: ref("a", "pods", "web-1"), Format: format})
		encoded, err := json.Marshal(out)
		require.NoError(t, err)
		assert.Less(t, len(encoded), 1024)
		assert.NotContains(t, string(encoded), "export-only-line")
		path := out["path"].(string)
		dir, err := e.srv.o.Downloads()
		require.NoError(t, err)
		assert.Equal(t, dir, filepath.Dir(path))
		assert.NotEqual(t, previous, path)
		previous = path
		info, err := os.Stat(path)
		require.NoError(t, err)
		assertPrivateExport(t, path)
		assert.Equal(t, float64(info.Size()), out["bytes"])
		assert.Equal(t, float64(6001), out["lines"])
		assert.Equal(t, true, out["complete"])
		contents, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Contains(t, string(contents), "export-only-line-6000")
		if format == "ndjson" {
			assert.True(t, json.Valid(bytes.SplitN(contents, []byte("\n"), 2)[0]))
		}
	}
}

func TestExportLogsHonorsFiltersAndReportsLimits(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	e.f.logs = func(_ context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		if err := sink.Source(1, "key", "web/main", "main"); err != nil {
			return err
		}
		return sink.Lines(1, []provider.LogLine{logLine(0, "error old"), logLine(1, "noise"), logLine(2, "error first"), logLine(3, "error second"), logLine(4, "error third")})
	}
	request := ExportLogsRequest{Ref: ref("a", "pods", "web-1"), SinceTime: logTime(1), UntilTime: logTime(5), Grep: &api.LogGrep{Pattern: "error"}, Limit: 2}
	out := e.ok("ExportLogs", request)
	assert.Equal(t, float64(2), out["lines"])
	assert.Equal(t, false, out["complete"])
	assert.Equal(t, "line_limit", out["stopReason"])
	contents, err := os.ReadFile(out["path"].(string))
	require.NoError(t, err)
	assert.Contains(t, string(contents), "error first")
	assert.NotContains(t, string(contents), "noise")
	assert.NotContains(t, string(contents), "old")
	assert.NotContains(t, string(contents), "third")
	request.Limit = 0
	status, _ := e.call("ExportLogs", request)
	assert.Equal(t, http.StatusBadRequest, status)
	e.refused("ExportLogs", ExportLogsRequest{Ref: ref("b", "pods", "api-1")})
	e.f.logs = func(_ context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		return sink.Lines(1, []provider.LogLine{logLine(1, "small"), logLine(2, strings.Repeat("x", 5000))})
	}
	out = e.ok("ExportLogs", ExportLogsRequest{Ref: ref("a", "pods", "web-1"), MaxBytes: 4096})
	assert.Equal(t, "byte_limit", out["stopReason"])
	assert.LessOrEqual(t, out["bytes"].(float64), float64(4096))
}

func TestRevocationRemovesAnIncompleteExport(t *testing.T) {
	e := newEnv(t)
	e.grant(one("a", agentgrant.VerbLogs))
	started := make(chan struct{})
	e.f.logs = func(ctx context.Context, _ core.Ref, _ provider.LogQuery, sink provider.LogSink) error {
		if err := sink.Lines(1, []provider.LogLine{logLine(1, "partial")}); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	body, err := json.Marshal(ExportLogsRequest{Ref: ref("a", "pods", "web-1")})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.srv.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/ExportLogs", bytes.NewReader(body)))
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	e.grant()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("export did not stop")
	}
	assert.Equal(t, http.StatusForbidden, w.Code)
	dir, err := e.srv.o.Downloads()
	require.NoError(t, err)
	files, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, files)
}
