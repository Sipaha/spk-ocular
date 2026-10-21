package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
)

type certificateStatusTestSession struct{ *argoTestSession }

func (s *certificateStatusTestSession) CertificateStatus(ctx context.Context, ref core.Ref) (core.CertificateStatus, error) {
	s.owner.calls.Add(1)
	return core.CertificateStatus{Ref: ref}, ctx.Err()
}

type certificateStatusTestProvider struct{ *argoTestProvider }

func (p *certificateStatusTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &certificateStatusTestSession{s.(*argoTestSession)}, nil
}
func TestCertificateStatusRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &certificateStatusTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "cert-manager.io/certificates", Scope: "blue", Name: "p", UID: "uid"}
	_, err := s.CertificateStatus(t.Context(), ref)
	require.Error(t, err)
	_, err = s.CertificateStatus(UIContext(t.Context()), ref)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.CertificateStatus(UIContext(t.Context()), ref)
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
			r.Scope = ""
		}
		_, err = s.CertificateStatus(UIContext(t.Context()), r)
		require.Error(t, err)
	}
	call, err := s.AgentCall(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	defer call.Done()
	_, err = s.CertificateStatus(call.ctx, ref)
	require.Error(t, err)
	require.EqualValues(t, 1, p.calls.Load())
}

func (s *certificateStatusTestSession) Get(_ context.Context, ref core.Ref) (*core.Resource, error) {
	return &core.Resource{Ref: ref, CertificateStatusAvailable: true}, nil
}
func TestCertificateStatusAvailabilityIsUIOnlyAndDoesNotCollect(t *testing.T) {
	p := &certificateStatusTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	_, err := s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	ref := core.Ref{Provider: "k", Target: "a", Scope: "blue", Kind: "cert-manager.io/certificates", Name: "web", UID: "uid"}
	ui, err := s.GetResource(UIContext(t.Context()), ref)
	require.NoError(t, err)
	require.True(t, ui.CertificateStatusAvailable)
	plain, err := s.GetResource(t.Context(), ref)
	require.NoError(t, err)
	require.False(t, plain.CertificateStatusAvailable)
	require.True(t, ui.CertificateStatusAvailable)
	require.Zero(t, p.calls.Load())
}
