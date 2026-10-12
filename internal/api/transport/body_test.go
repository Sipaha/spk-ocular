package transport

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRequestBodyBudgetBeforeService(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		for _, delta := range []int{-1, 0, 1} {
			t.Run(strings.Join([]string{map[bool]string{false: "known", true: "chunked"}[unknown], string(rune('1' + delta))}, "/"), func(t *testing.T) {
				called := false
				handler := handle(func(_ context.Context, _ *struct {
					Text string `json:"text"`
				}) (any, error) {
					called = true
					return nil, nil
				})
				envelope := len(`{"text":""}`)
				body := `{"text":"` + strings.Repeat("x", int(metadataBodyLimit)+delta-envelope) + `"}`
				req := httptest.NewRequest("POST", "/api/SetTargetState", strings.NewReader(body))
				if unknown {
					req.ContentLength = -1
				}
				recorder := httptest.NewRecorder()
				handler(recorder, req)
				require.Equal(t, delta <= 0, called)
				if delta > 0 {
					require.Equal(t, 400, recorder.Code)
				}
			})
		}
	}
}

func TestRequestBodyUnknownFieldsAndTrailingValues(t *testing.T) {
	for _, body := range []string{`{} {}`, `{} trailing`, `{"ignored":"` + strings.Repeat("x", int(metadataBodyLimit)) + `"}`} {
		called := false
		handler := handle(func(context.Context, *struct{}) (any, error) { called = true; return nil, nil })
		req := httptest.NewRequest("POST", "/api/ListTargets", strings.NewReader(body))
		req.ContentLength = -1
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		require.False(t, called)
		require.Equal(t, 400, recorder.Code)
	}
}

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (w *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestBodyDeadlineClearedBeforeOperation(t *testing.T) {
	writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler := handle(func(ctx context.Context, _ *struct{}) (any, error) {
		require.Len(t, writer.deadlines, 2)
		require.False(t, writer.deadlines[0].IsZero())
		require.True(t, writer.deadlines[1].IsZero())
		_, hasDeadline := ctx.Deadline()
		require.False(t, hasDeadline)
		return nil, nil
	})
	handler(writer, httptest.NewRequest("POST", "/api/ClusterSecurity", strings.NewReader("{}")))
	require.Equal(t, 200, writer.Code)
}

func TestEditBudgetPreservesEscapedDecodedTexts(t *testing.T) {
	text := strings.Repeat("\x00", 3<<20)
	body, err := json.Marshal(struct{ Original, Edited string }{text, text})
	require.NoError(t, err)
	called := false
	handler := handle(func(_ context.Context, r *struct{ Original, Edited string }) (any, error) {
		called = true
		require.Len(t, r.Original, 3<<20)
		require.Len(t, r.Edited, 3<<20)
		return nil, nil
	})
	recorder := httptest.NewRecorder()
	handler(recorder, httptest.NewRequest("POST", "/api/PrepareEdit", strings.NewReader(string(body))))
	require.True(t, called)
	require.Equal(t, 200, recorder.Code)
}
