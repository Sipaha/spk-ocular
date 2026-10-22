package api

import (
	"context"
	"testing"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type gatekeeperAuditTestSession struct{ *argoTestSession }

func (s *gatekeeperAuditTestSession) GatekeeperAudit(ctx context.Context, ref core.Ref) (core.GatekeeperAudit, error) {
	s.owner.calls.Add(1)
	return core.GatekeeperAudit{Ref: ref}, ctx.Err()
}

type gatekeeperAuditTestProvider struct{ *argoTestProvider }

func (p *gatekeeperAuditTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &gatekeeperAuditTestSession{s.(*argoTestSession)}, nil
}
func TestGatekeeperAuditRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &gatekeeperAuditTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "constraints.gatekeeper.sh/k8srequiredlabels", Name: "p", UID: "uid"}
	_, err := s.GatekeeperAudit(t.Context(), ref)
	require.Error(t, err)
	_, err = s.GatekeeperAudit(UIContext(t.Context()), ref)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.GatekeeperAudit(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.Equal(t, ref, out.Ref)
	for _, field := range []string{"uid", "name", "kind", "scope"} {
		r := ref
		switch field {
		case "uid":
			r.UID = ""
		case "name":
			r.Name = ""
		case "kind":
			r.Kind = ""
		case "scope":
			r.Scope = "foreign"
		}
		_, err = s.GatekeeperAudit(UIContext(t.Context()), r)
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.GatekeeperAudit(call.ctx, ref)
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
}

func (s *gatekeeperAuditTestSession) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: ref, GatekeeperAuditAvailable: true}, nil
}
func TestGatekeeperAuditAvailabilityIsUIOnlyAndDoesNotCollect(t *testing.T) {
	p := &gatekeeperAuditTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Kind: "constraints.gatekeeper.sh/k8srequiredlabels", Name: "web", UID: "uid"}
	ui, err := s.GetResource(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.True(t, ui.GatekeeperAuditAvailable)
	plain, err := s.GetResource(t.Context(), ref)
	require.NoError(t, err)
	require.False(t, plain.GatekeeperAuditAvailable)
	require.True(t, ui.GatekeeperAuditAvailable)
	require.Zero(t, p.calls.Load())
}
