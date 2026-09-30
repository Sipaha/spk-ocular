// Package compose is the Docker Compose provider: targets are Docker
// contexts (Engine endpoints, resolved as the docker CLI 29 does), scopes are
// Compose projects (the com.docker.compose.project label).
package compose

import (
	"context"
	"os"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const ProviderID = "compose"

// Provider discovers Docker contexts; the environment is re-read on every
// Discover.
type Provider struct {
	getenv func(string) string
	home   string

	mu   sync.Mutex
	last loaded // latest Discover result: sessions are built from it
}

// New reads the real environment and home dir.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return NewWith(os.Getenv, home)
}

// NewWith is New with an injected environment (tests).
func NewWith(getenv func(string) string, home string) *Provider {
	return &Provider{getenv: getenv, home: home}
}

func (p *Provider) ID() string    { return ProviderID }
func (p *Provider) Title() string { return "Docker Compose" }

func (p *Provider) env() Env { return ResolveEnv(p.getenv, p.home) }

var (
	_ provider.CommandAliaser = (*Provider)(nil)
	_ provider.ScopeNamer     = (*Provider)(nil)
	_ provider.TargetWatcher  = (*Provider)(nil)
)

func (p *Provider) CommandAliases() provider.CommandAliases {
	return provider.CommandAliases{Scope: []string{"project", "proj"}, Target: []string{"ctx", "context"}}
}

func (p *Provider) ScopeNames() core.ScopeNames {
	return core.ScopeNames{Singular: msg("scope.singular"), Plural: msg("scope.plural"), All: msg("scope.all")}
}

// Discover lists the contexts from local files only.
func (p *Provider) Discover(context.Context) (provider.Discovery, error) {
	l := load(p.env())
	p.mu.Lock()
	p.last = l
	p.mu.Unlock()
	var d provider.Discovery
	for _, c := range l.Contexts {
		d.Targets = append(d.Targets, target(c))
	}
	for _, pr := range l.Problems {
		d.Problems = append(d.Problems, core.Problem{Source: pr.Source, Message: pr.Message})
	}
	return d, nil
}

// targetID is stable: the context's name (a context is renamed only by
// being re-created).
func targetID(name string) string { return "context:" + name }

func target(c dockerContext) core.Target {
	t := core.Target{
		Provider:   ProviderID,
		ID:         targetID(c.Name),
		Title:      c.Name,
		Subtitle:   endpointShown(c.Host),
		Current:    c.Current,
		ConfigHash: c.Hash,
	}
	add := func(key, value string) {
		if value != "" {
			t.Details = append(t.Details, core.Detail{Key: key, Value: value})
		}
	}
	add("context", c.Name)
	add("description", c.Description)
	add("endpoint", endpointShown(c.Host))
	switch {
	case c.TLSError != "":
		add("tlsProblem", c.TLSError)
	case c.TLS != nil && c.TLS.SkipVerify:
		add("tls", "TLS without verification")
	case c.TLS != nil:
		add("tls", "TLS, verified")
	}
	add("meta", c.Source)
	return t
}

// contextByID is the latest discovered context with this target id.
func (p *Provider) contextByID(id string) (dockerContext, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.last.Contexts {
		if targetID(c.Name) == id {
			return c, true
		}
	}
	return dockerContext{}, false
}
