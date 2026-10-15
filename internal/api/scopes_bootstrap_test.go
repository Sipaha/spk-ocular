package api

import (
	"context"
	"errors"
	"testing"

	"github.com/spk/spk-ocular/internal/provider"
	"github.com/stretchr/testify/require"
)

type scopeCheckingSession struct {
	*fakeSession
	checkErr error
	checks   int
}

func (s *scopeCheckingSession) ScopeKind() string                 { return "namespaces" }
func (s *scopeCheckingSession) CheckScopes(context.Context) error { s.checks++; return s.checkErr }

type scopeCheckingProvider struct {
	*openable
	last *scopeCheckingSession
}

func (p *scopeCheckingProvider) Open(ctx context.Context, target string) (provider.Session, error) {
	s, err := p.openable.Open(ctx, target)
	if err != nil {
		return nil, err
	}
	p.last = &scopeCheckingSession{fakeSession: s.(*fakeSession)}
	p.last.scopesErr = errors.New("UI bootstrap must not read complete scope inventory")
	return p.last, nil
}

func TestLiveScopesBootstrapChecksAccessWithoutReadingCompleteInventory(t *testing.T) {
	p := &scopeCheckingProvider{openable: newOpenable("a")}
	s, _ := newService(t, p)
	scopes, err := s.ListScopes(context.Background(), "k", "a")
	require.NoError(t, err)
	require.Nil(t, scopes.Error)
	require.Empty(t, scopes.Scopes)
	require.NotNil(t, scopes.Scopes)
	require.Equal(t, "namespaces", scopes.Kind)
	require.Equal(t, 1, p.last.checks)
	p.last.checkErr = &provider.Error{Class: provider.ClassForbidden, Message: "scope list is forbidden"}
	scopes, err = s.ListScopes(context.Background(), "k", "a")
	require.NoError(t, err)
	require.NotNil(t, scopes.Error)
	require.Equal(t, "forbidden", scopes.Error.Code)
	require.Empty(t, scopes.Kind)
}
