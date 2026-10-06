// Package kubernetes exposes explicitly selected kubeconfig contexts. Linked
// files retain kubectl resolution rules; app-owned configs resolve in memory.
package kubernetes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const ProviderID = "kubernetes"

// Provider discovers registered contexts. Production uses WithConfigurations;
// an injected unmanaged provider retains raw kubectl discovery for loader tests.
type Provider struct {
	configs       *configurations
	configChanged chan struct{}
	getenv        func(string) string
	home          string
	shimPath      string // this binary, run as a bounded exec credential plugin
	// holdDir keeps the hold files of background sessions (the shim runs
	// their plugins headless, P18); "" — no holds.
	holdDir string
	// logSlots bounds pods/log requests open at once across all sessions.
	logSlots chan struct{}

	mu   sync.Mutex
	last loaded // latest Discover result; P1 builds clients from it
}

// New reads the real environment and home dir.
func New() *Provider {
	home, _ := os.UserHomeDir()
	return NewWith(os.Getenv, home)
}

// WithExecShim makes sessions run kubeconfig exec plugins through the shim
// at path (internal/execshim); "" disables it. holdDir keeps the hold files
// of background sessions ("" — none: plugins run as in the foreground).
func (p *Provider) WithExecShim(path, holdDir string) *Provider {
	p.shimPath, p.holdDir = path, holdDir
	return p
}

// NewWith is New with an injected environment (tests).
func NewWith(getenv func(string) string, home string) *Provider {
	return &Provider{getenv: getenv, home: home, logSlots: make(chan struct{}, maxLogRequests)}
}

func (p *Provider) ID() string    { return ProviderID }
func (p *Provider) Title() string { return "Kubernetes" }

var _ provider.CommandAliaser = (*Provider)(nil)

func (p *Provider) CommandAliases() provider.CommandAliases {
	return provider.CommandAliases{Scope: []string{"ns", "namespace"}, Target: []string{"ctx", "context"}}
}

var _ provider.ScopeNamer = (*Provider)(nil)

func (p *Provider) ScopeNames() core.ScopeNames {
	return core.ScopeNames{Singular: msg("scope.singular"), Plural: msg("scope.plural"), All: msg("scope.all")}
}

func (p *Provider) sources() Sources {
	if p.configs == nil {
		return ResolveSources(p.getenv, p.home)
	}
	m := p.configs
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Sources{}
	for _, l := range m.disk.Links {
		if l.Primary {
			s.Primary = append(s.Primary, l.Path)
		} else {
			s.Extra = append(s.Extra, l.Path)
		}
	}
	return s
}

func (p *Provider) Discover(context.Context) (provider.Discovery, error) {
	l := p.loadConfigured()
	p.mu.Lock()
	p.last = l
	for i := range p.last.Contexts {
		p.last.Contexts[i].Config = nil
	}
	p.mu.Unlock()
	var d provider.Discovery
	for _, kc := range l.Contexts {
		d.Targets = append(d.Targets, target(kc))
	}
	for _, pr := range l.Problems {
		d.Problems = append(d.Problems, core.Problem{Source: pr.Source, Message: pr.Message})
	}
	return d, nil
}

func target(kc kubeContext) core.Target {
	t := core.Target{
		Provider:     ProviderID,
		Encrypted:    kc.Encrypted,
		Locked:       kc.Locked,
		ID:           kc.ID,
		Title:        kc.Name,
		Subtitle:     kc.Cluster,
		Current:      kc.Current,
		ConfigHash:   kc.Hash,
		Identity:     kc.Identity,
		DefaultScope: kc.Namespace,
	}
	if kc.DisplayName != "" {
		t.Title = kc.DisplayName
	}
	add := func(key, value string) {
		if value != "" {
			t.Details = append(t.Details, core.Detail{Key: key, Value: value})
		}
	}
	add("context", kc.Name)
	add("cluster", kc.Cluster)
	add("server", withoutUserinfo(kc.Server))
	add("user", kc.User)
	add("auth", kc.Auth)
	add("defaultNamespace", kc.Namespace)
	add("file", kc.DefinedIn)
	if kc.Extra {
		// Contexts from standalone files in ~/.kube are not what kubectl
		// sees; the file name tells them apart (and from same-named ones).
		t.Subtitle = strings.TrimPrefix(t.Subtitle+" · "+filepath.Base(kc.DefinedIn), " · ")
	}
	return t
}
