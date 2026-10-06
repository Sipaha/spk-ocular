package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"k8s.io/klog/v2"

	"github.com/spk/spk-ocular/internal/agentapi"
	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/providers/compose"
	"github.com/spk/spk-ocular/internal/providers/kubernetes"
	"github.com/spk/spk-ocular/internal/providers/synthetic"
	"github.com/spk/spk-ocular/internal/store"
)

// appCore is what both modes share: data dir, store, providers, the API service.
type appCore struct {
	// Synthetic is the test provider (--test-synthetic), else nil.
	Synthetic *synthetic.Provider
	Paths     paths.Paths
	Store     *store.Store
	Emitter   *events.Emitter
	Service   *api.Service
	// Agent serves agent access over a unix socket (P14).
	Agent *agentapi.Server
}

// newCore wires everything and starts the service's watchers. Nothing here
// touches the network: startup stays fast with an unreachable cluster.
func newCore(ctx context.Context, mode string, withSynthetic bool) (*appCore, error) {
	quietClientGo()
	p, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	if err := p.Ensure(); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, p.DBFile)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	self, _ := os.Executable() // runs kubeconfig exec plugins with a timeout (internal/execshim)
	// Background sessions' plugins run headless while their hold file is
	// in run/ (P18).
	providers := []provider.Provider{kubernetes.New().WithExecShim(self, filepath.Join(p.DataDir, "run")), compose.New()}
	var syn *synthetic.Provider
	if withSynthetic {
		syn = synthetic.New()
		// A second synthetic target (--test-api only): lets e2e switch
		// between two warm targets (P18).
		if os.Getenv("SPK_OCULAR_TEST_SYNTH_SECOND") != "" {
			syn.EnableSecondTarget()
		}
		providers = append(providers, syn)
	}
	reg, err := provider.NewRegistry(providers...)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	em := events.NewEmitter()
	svc := api.NewService(reg, st, em, api.Options{DataDir: p.DataDir, Version: version, Mode: mode, Getenv: os.Getenv})
	home, _ := os.UserHomeDir()
	agent := agentapi.New(agentapi.Options{Service: svc, Store: st, Socket: p.AgentSocket, Lock: p.AgentLock, Version: version, Home: home, Downloads: func() (string, error) { return paths.Downloads(os.Getenv) }})
	svc.SetAgentControl(agent)
	svc.Start(ctx)
	// A socket that cannot be served is not fatal: the UI shows why
	// (AgentAccessStatus), the app works without agent access.
	if err := agent.Start(); err != nil {
		slog.Warn("agent access unavailable", "socket", p.AgentSocket, "err", err)
	}
	return &appCore{Synthetic: syn, Paths: p, Store: st, Emitter: em, Service: svc, Agent: agent}, nil
}

// Close stops agent access (its writes use the service), then the service,
// then the store they write to.
func (c *appCore) Close() {
	c.Agent.Close()
	c.Service.Close()
	_ = c.Store.Close()
}

// quietClientGo silences client-go's klog output (every failed watch retry
// is an error line on stderr): the same failures reach the UI as view
// statuses. SPK_OCULAR_KLOG=1 keeps it for debugging.
func quietClientGo() {
	if os.Getenv("SPK_OCULAR_KLOG") == "" {
		klog.SetSlogLogger(slog.New(slog.DiscardHandler))
	}
}
