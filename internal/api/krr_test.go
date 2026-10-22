package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/spk/spk-ocular/internal/krr"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestKRRBackendOwnsProjectedReportsWithoutConnection(t *testing.T) {
	p := &argoTestProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	b, e := os.ReadFile("../krr/testdata/simple.json")
	require.NoError(t, e)
	_, e = s.KRR(t.Context(), KRRRequest{Command: "import", Version: "1.31.0", Data: b})
	require.Error(t, e)
	agentService, _ := newService(t, &argoTestProvider{openable: newOpenable("a")})
	call, e := agentService.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, e)
	defer call.Done()
	_, e = s.KRR(call.ctx, KRRRequest{Command: "status"})
	require.Error(t, e)
	imported, e := s.KRR(UIContext(t.Context()), KRRRequest{Command: "import", Version: "1.31.0", Data: b})
	require.NoError(t, e)
	require.NotNil(t, imported.Selected)
	id := imported.Selected.Summary.ID
	safe, e := json.Marshal(imported)
	require.NoError(t, e)
	require.NotContains(t, string(safe), "PRIVATE-")
	require.Empty(t, p.opened)
	// A second independent UI request can retrieve the same backend-owned report.
	got, e := s.KRR(UIContext(t.Context()), KRRRequest{Command: "get", ID: id})
	require.NoError(t, e)
	require.Equal(t, "app", got.Selected.Report.Rows[0].Name)
	for _, req := range []KRRRequest{{Command: "status", Data: b}, {Command: "import", Version: "old", Data: b}, {Command: "get", ID: id, Version: "1.31.0"}, {Command: "unknown"}, {Command: "import", Version: "1.31.0", Data: []byte(`{"credential":"PRIVATE-ERROR"}`)}} {
		_, e = s.KRR(UIContext(t.Context()), req)
		require.Error(t, e)
		require.NotContains(t, e.Error(), "PRIVATE-")
	}
	_, e = s.KRR(UIContext(t.Context()), KRRRequest{Command: "forget", ID: id})
	require.NoError(t, e)
	_, e = s.KRR(UIContext(t.Context()), KRRRequest{Command: "get", ID: id})
	require.Error(t, e)
	require.Empty(t, p.opened)
}

type krrAPIProvider struct {
	*openable
	starts atomic.Int32
}

func (p *krrAPIProvider) ID() string { return "kubernetes" }
func (p *krrAPIProvider) Open(ctx context.Context, id string) (provider.Session, error) {
	sess, e := p.openable.Open(ctx, id)
	if e != nil {
		return nil, e
	}
	return &krrAPISession{fakeSession: sess.(*fakeSession), p: p}, nil
}

type krrAPISession struct {
	*fakeSession
	p *krrAPIProvider
}

func (s *krrAPISession) ReviewKRR(_ context.Context, o krr.RunOptions) (krr.Review, error) {
	return krr.Review{Options: o, Expect: "review-token", Image: krr.Image, NamespaceUID: "ns", ServiceAccountUID: "sa", Name: "ocular-krr-fixture", DeadlineSeconds: 600}, nil
}
func (s *krrAPISession) PrepareKRR(_ context.Context, _ krr.Review, _ provider.HelperJournal) (krr.Execution, error) {
	return krrAPIExecution{p: s.p}, nil
}

type krrAPIExecution struct{ p *krrAPIProvider }

func (e krrAPIExecution) Execute(ctx context.Context) ([]byte, error) {
	e.p.starts.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestKRRBackendRunSurvivesRequestAndRequiresExplicitConnection(t *testing.T) {
	p := &krrAPIProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	options := krr.RunOptions{Namespace: "tools", ServiceAccount: "reader", Source: "http://metrics.invalid", SingleCluster: true, ReviewedAccount: true, NoSourceAuth: true}
	_, e := s.ListTargets(ctx)
	require.NoError(t, e)
	_, e = s.KRR(ctx, KRRRequest{Command: "review", Provider: "kubernetes", Target: "a", Options: &options})
	require.Error(t, e)
	require.Empty(t, p.opened)
	_, e = s.ConnectTarget(ctx, "kubernetes", "a")
	require.NoError(t, e)
	awaitConnection(t, s, "connected")
	response, e := s.KRR(ctx, KRRRequest{Command: "review", Provider: "kubernetes", Target: "a", Options: &options})
	require.NoError(t, e)
	require.NotNil(t, response.Review)
	stale := *response.Review
	stale.ConfigRev = "stale"
	_, e = s.KRR(ctx, KRRRequest{Command: "start", Provider: "kubernetes", Target: "a", Review: &stale})
	require.Error(t, e)
	require.Zero(t, p.starts.Load())
	request, abort := context.WithCancel(ctx)
	response, e = s.KRR(request, KRRRequest{Command: "start", Provider: "kubernetes", Target: "a", Review: response.Review})
	require.NoError(t, e)
	require.Len(t, response.Runs, 1)
	id := response.Runs[0].ID
	abort()
	require.Eventually(t, func() bool { return p.starts.Load() == 1 }, time.Second, time.Millisecond)
	response, e = s.KRR(ctx, KRRRequest{Command: "status"})
	require.NoError(t, e)
	require.Equal(t, "running", response.Runs[0].State)
	require.Equal(t, "a", response.Runs[0].Target)
	_, e = s.KRR(ctx, KRRRequest{Command: "cancel", ID: id})
	require.NoError(t, e)
	require.Eventually(t, func() bool { return s.krrRuns.List()[0].State == "cancelled" }, time.Second, time.Millisecond)
	for _, req := range []KRRRequest{{Command: "status", Provider: "kubernetes"}, {Command: "cancel", ID: id, Options: &options}, {Command: "review", Provider: "kubernetes", Target: "a", Options: &options, Data: []byte("x")}, {Command: "start", Provider: "kubernetes", Target: "a", Review: &stale, Options: &options}} {
		_, e = s.KRR(ctx, req)
		require.Error(t, e)
	}
}
func TestKRRRecoveryStatusAndSafeJournalProjection(t *testing.T) {
	p := &recoveryProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	h := recoveryHelper()
	h.Kind = "krr-job"
	h.NodeName = ""
	h.NodeUID = ""
	h.ServiceAccount = "reader"
	h.ServiceAccountUID = "sa"
	h.ContractSHA = strings.Repeat("a", 64)
	require.NoError(t, s.store.InsertHelper(t.Context(), store.HelperRecord{Helper: h, Owner: "crashed", NextTry: 0, Delay: 60000}))
	response, e := s.KRR(UIContext(t.Context()), KRRRequest{Command: "status"})
	require.NoError(t, e)
	require.Len(t, response.Recovery, 1)
	require.Empty(t, p.opened)
	safe, e := json.Marshal(response)
	require.NoError(t, e)
	require.NotContains(t, string(safe), h.ContractSHA)
	require.NotContains(t, string(safe), "serviceAccountUID")
	s.reapOwnedHelpers(t.Context())
	require.Zero(t, p.calls.Load())
	connectRecovery(t, s)
	s.reapOwnedHelpers(t.Context())
	response, e = s.KRR(UIContext(t.Context()), KRRRequest{Command: "status"})
	require.NoError(t, e)
	require.Empty(t, response.Recovery)
}

func TestKRRAdmissionCountsDurableIntentsAndNotYetJournaledRuns(t *testing.T) {
	p := &krrAPIProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	ctx := UIContext(t.Context())
	_, e := s.ListTargets(ctx)
	require.NoError(t, e)
	_, e = s.ConnectTarget(ctx, "kubernetes", "a")
	require.NoError(t, e)
	awaitConnection(t, s, "connected")
	for i := 0; i < 7; i++ {
		h := recoveryHelper()
		h.ID = fmt.Sprintf("pending-%d", i)
		h.Name = h.ID
		h.Kind = "krr-job"
		require.NoError(t, s.store.InsertHelper(ctx, store.HelperRecord{Helper: h, Owner: "crashed", NextTry: 0, Delay: 60000}))
	}
	options := krr.RunOptions{Namespace: "tools", ServiceAccount: "reader", Source: "http://metrics.invalid", SingleCluster: true, ReviewedAccount: true, NoSourceAuth: true}
	response, e := s.KRR(ctx, KRRRequest{Command: "review", Provider: "kubernetes", Target: "a", Options: &options})
	require.NoError(t, e)
	started, e := s.KRR(ctx, KRRRequest{Command: "start", Provider: "kubernetes", Target: "a", Review: response.Review})
	require.NoError(t, e)
	require.Len(t, started.Runs, 1)
	_, e = s.KRR(ctx, KRRRequest{Command: "start", Provider: "kubernetes", Target: "a", Review: response.Review})
	require.ErrorContains(t, e, "outstanding helper intents")
}
