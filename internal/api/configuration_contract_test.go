package api

import (
	"context"
	"testing"

	"github.com/spk/spk-ocular/internal/configuration"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/stretchr/testify/require"
)

// A decorator preserves configuration capability without exposing a concrete
// provider type to the API. It deliberately embeds only the generic contract.
type configuredProvider struct {
	provider.Provider
	source configuration.Source
	calls  int
}

func (p *configuredProvider) Configurations(r configuration.ConfigRequest) (configuration.ConfigState, error) {
	p.calls++
	return p.source.Configurations(r)
}
func (p *configuredProvider) ConfigurationFile(r configuration.ConfigRequest) (string, error) {
	return p.source.ConfigurationFile(r)
}

func TestConfigurationsCapabilitySurvivesProviderDecorator(t *testing.T) {
	p, err := kubernetes.NewWith(func(string) string { return "" }, t.TempDir()).WithConfigurations(t.TempDir())
	require.NoError(t, err)
	wrapper := &configuredProvider{Provider: p, source: p}
	s, _ := newService(t, wrapper)
	_, err = s.Configurations(context.Background(), configuration.ConfigRequest{Command: "status"})
	require.True(t, IsCoded(err, "forbidden"))
	require.Zero(t, wrapper.calls)
	out, err := s.Configurations(UIContext(t.Context()), configuration.ConfigRequest{Command: "status"})
	require.NoError(t, err)
	require.NotNil(t, out.Candidates)
	require.Equal(t, 1, wrapper.calls)
	// Exercise actual encryption capability rather than a status-only fake.
	_, err = s.Configurations(UIContext(t.Context()), configuration.ConfigRequest{Command: "setup", Password: "fixture password"})
	require.NoError(t, err)
	out, err = s.Configurations(UIContext(t.Context()), configuration.ConfigRequest{Command: "status"})
	require.NoError(t, err)
	require.True(t, out.Encrypted)
}

func TestConfigurationsMissingCapabilityIsNotSuccessfulInitialization(t *testing.T) {
	s, _ := newService(t, &fakeProvider{id: configuration.KubernetesProviderID})
	_, err := s.Configurations(UIContext(t.Context()), configuration.ConfigRequest{Command: "status"})
	require.True(t, IsCoded(err, CodeUnsupported))
}
