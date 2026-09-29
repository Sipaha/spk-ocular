package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// wireServer is an API server for one object that counts the writes it
// gets; reply decides each write's answer (after applying it or not).
type wireServer struct {
	mu      sync.Mutex
	obj     *unstructured.Unstructured
	writes  int
	applied int
	reply   func(n int, w http.ResponseWriter) (apply, answered bool)
}

func statusReply(w http.ResponseWriter, code int, reason string, retryAfter bool) {
	if retryAfter {
		w.Header().Set("Retry-After", "0")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, `{"apiVersion":"v1","kind":"Status","status":"Failure","reason":%q,"code":%d,"message":"test"}`, reason, code)
}

func (ws *wireServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ws.obj)
		return
	}
	_, _ = io.Copy(io.Discard, r.Body)
	ws.writes++
	apply, answered := ws.reply(ws.writes, w)
	if apply {
		ws.applied++
		ws.obj.SetResourceVersion(fmt.Sprint(100 + ws.applied))
	}
	if !answered {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ws.obj)
	}
}

// wireSession is a session built like Open builds one, against ws.
func wireSession(t *testing.T, ws *wireServer) *session {
	t.Helper()
	srv := httptest.NewServer(ws)
	t.Cleanup(srv.Close)
	s, err := sessionFor(&rest.Config{Host: srv.URL}, "ctx", "ctx", "h")
	require.NoError(t, err)
	t.Cleanup(s.Close)
	return s
}

// An ambiguous answer (5xx with Retry-After, as a proxy may give after the
// server applied the write) must not make client-go send the write again:
// one request, and the outcome is unknown.
func TestAnActionWriteIsOneRequestAndA5xxIsUnknown(t *testing.T) {
	for _, a := range []struct {
		action string
		p      core.ActionParams
	}{{"restart", core.ActionParams{}}, {"scale", count(3)}, {"delete", core.ActionParams{}}} {
		t.Run(a.action, func(t *testing.T) {
			ws := &wireServer{obj: workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)}),
				reply: func(_ int, w http.ResponseWriter) (bool, bool) {
					statusReply(w, http.StatusInternalServerError, "InternalError", true)
					return true, true
				}}
			s := wireSession(t, ws)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			expect := actionExpect(deploymentsKind, a.action, a.p, ws.obj)
			_, err := s.RunAction(ctx, provider.ActionRun{Ref: deployWebRef, Action: a.action, Params: a.p, Expect: expect})
			assertClass(t, err, provider.ClassUnknown)
			assert.Equal(t, 1, ws.writes, "one write request")
			assert.Equal(t, 1, ws.applied)
		})
	}
}

func TestAnAPITimeoutOrUnavailableIsUnknownAndTooManyRequestsIsNot(t *testing.T) {
	for name, c := range map[string]struct {
		code   int
		reason string
		class  provider.ErrorClass
	}{
		"timeout":             {http.StatusGatewayTimeout, "Timeout", provider.ClassUnknown},
		"server timeout":      {http.StatusInternalServerError, "ServerTimeout", provider.ClassUnknown},
		"service unavailable": {http.StatusServiceUnavailable, "ServiceUnavailable", provider.ClassUnknown},
		"bad gateway":         {http.StatusBadGateway, "", provider.ClassUnknown},
		"too many requests":   {http.StatusTooManyRequests, "TooManyRequests", provider.ClassUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			ws := &wireServer{obj: workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)}),
				reply: func(_ int, w http.ResponseWriter) (bool, bool) {
					statusReply(w, c.code, c.reason, true)
					return false, true
				}}
			s := wireSession(t, ws)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := s.RunAction(ctx, provider.ActionRun{Ref: deployWebRef, Action: "restart", Expect: actionExpect(deploymentsKind, "restart", core.ActionParams{}, ws.obj)})
			assertClass(t, err, c.class)
			assert.Equal(t, 1, ws.writes)
		})
	}
}

// Our own retry stays: a 409 whose reread proves nothing was written.
func TestAProvenVersionOnlyConflictIsStillRetriedOnTheWire(t *testing.T) {
	ws := &wireServer{obj: workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)})}
	ws.reply = func(n int, w http.ResponseWriter) (bool, bool) {
		if n == 1 {
			ws.obj.SetResourceVersion("2") // status churn
			statusReply(w, http.StatusConflict, "Conflict", false)
			return false, true
		}
		return true, false
	}
	s := wireSession(t, ws)
	_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: "restart", Expect: actionExpect(deploymentsKind, "restart", core.ActionParams{}, ws.obj)})
	require.NoError(t, err)
	assert.Equal(t, 2, ws.writes)
	assert.Equal(t, 1, ws.applied)
}

// A validation or admission refusal (422) stays a refusal even when the
// version moved meanwhile (unrelated status churn): no second write.
func TestARefusalIsNotRetriedOnTheWireEvenWhenTheVersionMoved(t *testing.T) {
	for _, a := range []struct {
		action string
		p      core.ActionParams
	}{{"restart", core.ActionParams{}}, {"scale", count(3)}} {
		t.Run(a.action, func(t *testing.T) {
			ws := &wireServer{obj: workload("Deployment", "web", "uid-web", "1", map[string]any{"replicas": int64(2)})}
			ws.reply = func(n int, w http.ResponseWriter) (bool, bool) {
				if n == 1 {
					ws.obj.SetResourceVersion("2") // status churn
					statusReply(w, http.StatusUnprocessableEntity, "Invalid", false)
					return false, true
				}
				return true, false
			}
			s := wireSession(t, ws)
			_, err := s.RunAction(context.Background(), provider.ActionRun{Ref: deployWebRef, Action: a.action, Params: a.p, Expect: actionExpect(deploymentsKind, a.action, a.p, ws.obj)})
			assertClass(t, err, provider.ClassInvalid)
			assert.Equal(t, 1, ws.writes)
			assert.Equal(t, 0, ws.applied)
		})
	}
}
