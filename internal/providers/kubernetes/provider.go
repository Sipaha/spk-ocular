// Package kubernetes is the Kubernetes provider: targets are kubeconfig
// contexts, reached exactly the way kubectl reaches them.
package kubernetes

import (
	"context"
	"os"
	"sync"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

const ProviderID = "kubernetes"

// Provider discovers kube contexts. Sources are re-resolved on every
// Discover, so a file added to ~/.kube later shows up.
type Provider struct {
	getenv func(string) string
	home   string

	mu   sync.Mutex
	last loaded // latest Discover result; P1 builds clients from it
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
func (p *Provider) Title() string { return "Kubernetes" }

func (p *Provider) sources() Sources { return ResolveSources(p.getenv, p.home) }

func (p *Provider) Discover(context.Context) (provider.Discovery, error) {
	l := load(p.sources())
	p.mu.Lock()
	p.last = l
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
		Provider: ProviderID,
		ID:       kc.ID,
		Title:    kc.ID,
		Subtitle: kc.Cluster,
		Current:  kc.Current,
	}
	add := func(key, value string) {
		if value != "" {
			t.Details = append(t.Details, core.Detail{Key: key, Value: value})
		}
	}
	add("context", kc.Name)
	add("cluster", kc.Cluster)
	add("server", kc.Server)
	add("user", kc.User)
	add("auth", kc.Auth)
	add("namespace", kc.Namespace)
	add("file", kc.DefinedIn)
	return t
}
