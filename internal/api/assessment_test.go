package api

import (
	"context"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type assessmentTestSession struct {
	*argoTestSession
	onRead func(context.Context)
}

func (s *assessmentTestSession) AssessNamespace(ctx context.Context, req core.NamespaceAssessmentRequest) (core.NamespaceAssessment, error) {
	s.owner.calls.Add(1)
	if s.onRead != nil {
		s.onRead(ctx)
	}
	return core.NamespaceAssessment{Namespace: req.Namespace, Check: req.Check}, nil
}

type assessmentTestProvider struct {
	*argoTestProvider
	onRead func(context.Context)
}

func (p *assessmentTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &assessmentTestSession{s.(*argoTestSession), p.onRead}, nil
}
func TestNamespaceAssessmentUIOnlyExplicitConnectionAndSelection(t *testing.T) {
	p := &assessmentTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	req := core.NamespaceAssessmentRequest{Provider: "k", Target: "a", Namespace: "blue", Check: "configuration"}
	_, err := s.AssessNamespace(t.Context(), req)
	require.Error(t, err)
	_, err = s.AssessNamespace(UIContext(t.Context()), req)
	require.Error(t, err)
	require.Empty(t, p.opened)
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.AssessNamespace(UIContext(t.Context()), req)
	require.NoError(t, err)
	require.Equal(t, "blue", out.Namespace)
	for _, bad := range []core.NamespaceAssessmentRequest{{Provider: "k", Target: "a", Namespace: "", Check: "configuration"}, {Provider: "k", Target: "a", Namespace: "*", Check: "vpa"}, {Provider: "k", Target: "a", Namespace: "blue", Check: "trivy"}} {
		_, err = s.AssessNamespace(UIContext(t.Context()), bad)
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.AssessNamespace(call.ctx, req)
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
	ui, err := s.ListKinds(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	require.Contains(t, ui.Kinds, assessmentKind())
	agent, err := s.ListKinds(t.Context(), "k", "a")
	require.NoError(t, err)
	require.NotContains(t, agent.Kinds, assessmentKind())
}
func TestNamespaceAssessmentDiscardsLateConnectionAndCancellation(t *testing.T) {
	for _, mode := range []string{"cancel", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			p := &assessmentTestProvider{argoTestProvider: &argoTestProvider{openable: newOpenable("a")}}
			s, _ := newService(t, p)
			ctx, cancel := context.WithCancel(UIContext(t.Context()))
			defer cancel()
			p.onRead = func(context.Context) {
				if mode == "cancel" {
					cancel()
				} else {
					require.NoError(t, s.CloseTarget(UIContext(t.Context()), "k", "a"))
				}
			}
			_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
			require.NoError(t, err)
			awaitConnection(t, s, "connected")
			out, err := s.AssessNamespace(ctx, core.NamespaceAssessmentRequest{Provider: "k", Target: "a", Namespace: "blue", Check: "configuration"})
			require.Error(t, err)
			require.Empty(t, out.Namespace)
		})
	}
}
