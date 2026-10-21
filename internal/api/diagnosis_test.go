package api

import (
	"context"
	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
	"testing"
)

type diagnosisTestSession struct{ *argoTestSession }

func (s *diagnosisTestSession) DiagnoseResource(ctx context.Context, ref core.Ref) (core.Diagnosis, error) {
	s.owner.calls.Add(1)
	return core.Diagnosis{Ref: ref}, ctx.Err()
}

type diagnosisTestProvider struct{ *argoTestProvider }

func (p *diagnosisTestProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, e := p.argoTestProvider.Open(ctx, target)
	if e != nil {
		return nil, e
	}
	return &diagnosisTestSession{s.(*argoTestSession)}, nil
}
func TestDiagnosisRequiresUIConnectionAndPinnedIdentity(t *testing.T) {
	p := &diagnosisTestProvider{&argoTestProvider{openable: newOpenable("a")}}
	s, _ := newService(t, p)
	ref := core.Ref{Provider: "k", Target: "a", Kind: "pods", Scope: "blue", Name: "p", UID: "uid"}
	_, err := s.DiagnoseResource(t.Context(), ref)
	require.Error(t, err)
	_, err = s.DiagnoseResource(UIContext(t.Context()), ref)
	require.Error(t, err)
	require.Empty(t, p.opened)
	require.Zero(t, p.calls.Load())
	_, err = s.ConnectTarget(UIContext(t.Context()), "k", "a")
	require.NoError(t, err)
	awaitConnection(t, s, "connected")
	out, err := s.DiagnoseResource(UIContext(t.Context()), ref)
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
		_, err = s.DiagnoseResource(UIContext(t.Context()), r)
		require.Error(t, err)
	}
	require.EqualValues(t, 1, p.calls.Load())
}
