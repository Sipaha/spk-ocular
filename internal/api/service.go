package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/forwards"
	"github.com/spk/spk-ocular/internal/helm"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/spk/spk-ocular/internal/streams"
	"github.com/spk/spk-ocular/internal/views"
)

const prefSelectedTarget = "selected_target"

type Options struct {
	Version string
	DataDir string
	Mode    string // "desktop" | "browser"
	Getenv  func(string) string
}

// Service implements API.
type Service struct {
	helmRepos     *helm.Repositories
	helmPlans     *helm.Plans
	reg           *provider.Registry
	store         *store.Store
	em            *events.Emitter
	opts          Options
	favoritesMu   sync.Mutex
	navSectionsMu sync.Mutex

	views      *views.Manager
	streams    *streams.Registry
	streamBase func() (string, error)
	terms      termProtos
	fwd        *forwards.Manager

	sessMu        sync.Mutex
	sessions      map[string]*sessionEntry // by ownerKey
	reaper        *time.Timer
	sessSeq       uint64
	now           func() time.Time
	connections   map[string]*connectionAttempt
	connectionSeq uint64
	closed        bool
	// current: the selected target's key ("" until the first selection
	// in this run); left: when each recent target was left (at most
	// recentTargets of them).
	current string
	left    map[string]time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// metricGates: per view, the metrics request in flight and the oldest
	// seq still accepted (see GetMetrics).
	metricsMu   sync.Mutex
	metricGates map[string]*metricGate
	// beforeMetricsGate (tests): runs between reading a view and taking
	// its metrics gate.
	beforeMetricsGate func()

	// agent is the agent socket's side the UI manages (nil: not served).
	agent AgentControl
	// agentWatches: the slots of agents' snapshot watches (AgentCall).
	agentWatches chan struct{}

	// revKey keys ConfigRev: configuration hashes cover credentials, so the
	// page gets only a keyed digest it cannot test guesses against.
	revKey []byte
}

var _ API = (*Service)(nil)

func NewService(reg *provider.Registry, st *store.Store, em *events.Emitter, o Options) *Service {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return &Service{helmRepos: helm.NewRepositories(helm.SettingsDirectory(o.DataDir)), helmPlans: helm.NewPlans(), reg: reg, store: st, em: em, opts: o, views: views.NewManager(em), streams: streams.NewRegistry(), fwd: newForwards(em), sessions: map[string]*sessionEntry{}, connections: map[string]*connectionAttempt{}, left: map[string]time.Time{}, now: time.Now, revKey: key, agentWatches: make(chan struct{}, maxAgentWatches)}
}

// configRev is the opaque revision of a configuration hash ("" for none).
func (s *Service) configRev(hash string) string {
	if hash == "" {
		return ""
	}
	m := hmac.New(sha256.New, s.revKey)
	m.Write([]byte(hash))
	return hex.EncodeToString(m.Sum(nil)[:8])
}

// live is t as the UI sees it: with its revision.
func (s *Service) live(t core.LiveTarget) core.LiveTarget {
	t.ConfigRev = s.configRev(t.ConfigHash)
	return t
}

// Start begins watching local configuration of providers that support it.
// It never blocks on the network.
func (s *Service) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	for _, p := range s.reg.All() {
		w, ok := p.(provider.TargetWatcher)
		if !ok {
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			err := w.Watch(ctx, func() {
				s.revalidateSessions(ctx, p.ID())
				s.em.Emit(events.Event{Type: EventTargetsChanged, Key: p.ID(), Payload: map[string]any{"provider": p.ID()}})
			})
			if err != nil {
				slog.Warn("target watch stopped; the list refreshes only on demand", "provider", p.ID(), "err", err)
			}
		}()
	}
}

// Close stops the watchers, views and sessions.
func (s *Service) Close() {
	defer func() {
		for _, p := range s.reg.All() {
			if c, ok := p.(interface{ CloseConfigurations() }); ok {
				c.CloseConfigurations()
			}
		}
	}()
	s.helmPlans.Close()
	s.stopConnections()
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.views.CloseAll()
	s.streams.Close()
	s.terms.closeAll()
	s.fwd.Close()
	s.sessMu.Lock()
	if s.reaper != nil {
		s.reaper.Stop()
	}
	for key := range s.sessions {
		s.closeSessionLocked(key)
	}
	s.sessMu.Unlock()
}

func (s *Service) AppInfo(ctx context.Context) (AppInfo, error) {
	preference, err := s.store.GetUIPref(ctx, prefLanguage)
	if err != nil {
		return AppInfo{}, coded(CodeInternal, err)
	}
	if supportedLanguage(preference) != preference {
		preference = ""
	}
	system := systemLanguage(s.opts.Getenv)
	language := preference
	if language == "" {
		language = uiLanguage(s.opts.Getenv)
	}
	return AppInfo{
		Name:               "SPK Ocular",
		Version:            s.opts.Version,
		Mode:               s.opts.Mode,
		Language:           language,
		LanguagePreference: preference,
		SystemLanguage:     system,
	}, nil
}

func (s *Service) ListTargets(ctx context.Context) (TargetsView, error) {
	view := TargetsView{Groups: []TargetGroup{}}
	sel, err := s.selected(ctx)
	if err != nil {
		return view, coded(CodeInternal, err)
	}
	for _, p := range s.reg.All() {
		g := TargetGroup{Provider: p.ID(), Title: p.Title(), Targets: []core.Target{}, Problems: []core.Problem{}}
		if a, ok := p.(provider.CommandAliaser); ok {
			g.Aliases = a.CommandAliases()
		}
		if n, ok := p.(provider.ScopeNamer); ok {
			names := n.ScopeNames()
			g.ScopeNames = &names
		}
		d, err := p.Discover(ctx)
		if err != nil {
			g.Error = err.Error()
		} else {
			g.Targets = append(g.Targets, d.Targets...)
			g.Problems = append(g.Problems, d.Problems...)
		}
		s.sessMu.Lock()
		for i := range g.Targets {
			g.Targets[i].ConfigRev = s.configRev(g.Targets[i].ConfigHash)
			e := s.sessions[ownerKey(p.ID(), g.Targets[i].ID)]
			g.Targets[i].Open = e != nil && !e.closing
			if a := s.connections[ownerKey(p.ID(), g.Targets[i].ID)]; a != nil {
				status := a.status
				g.Targets[i].Connection = &status
			}
		}
		s.sessMu.Unlock()
		for _, t := range g.Targets {
			if sel != nil && sel.Provider == t.Provider && sel.ID == t.ID {
				view.Selected = sel
			}
		}
		view.Groups = append(view.Groups, g)
	}
	return view, nil
}

func (s *Service) SelectTarget(ctx context.Context, providerID, id string) error {
	p, ok := s.reg.Get(providerID)
	if !ok {
		return coded(CodeNotFound, fmt.Errorf("unknown provider %q", providerID))
	}
	d, err := p.Discover(ctx)
	if err != nil {
		return coded(CodeInternal, err)
	}
	found := false
	for _, t := range d.Targets {
		found = found || t.ID == id
	}
	if !found {
		return coded(CodeNotFound, fmt.Errorf("no target %q in %s", id, p.Title()))
	}
	prev := ""
	if sel, err := s.selected(ctx); err == nil && sel != nil {
		prev = ownerKey(sel.Provider, sel.ID)
	}
	b, _ := json.Marshal(TargetRef{Provider: providerID, ID: id})
	if err := s.store.SetUIPref(ctx, prefSelectedTarget, string(b)); err != nil {
		return coded(CodeInternal, err)
	}
	s.selectSession(prev, ownerKey(providerID, id))
	return nil
}

func (s *Service) selected(ctx context.Context) (*TargetRef, error) {
	raw, err := s.store.GetUIPref(ctx, prefSelectedTarget)
	if err != nil || raw == "" {
		return nil, err
	}
	var ref TargetRef
	if err := json.Unmarshal([]byte(raw), &ref); err != nil {
		slog.Warn("ignoring an unreadable remembered target", "err", err)
		return nil, nil
	}
	return &ref, nil
}

// uiLanguage returns the supported system language, or English.
func uiLanguage(getenv func(string) string) string {
	if language := systemLanguage(getenv); language != "" {
		return language
	}
	return "en"
}

// IsCoded reports whether err is a CodedError with the given code.
func IsCoded(err error, code string) bool {
	var ce *CodedError
	return errors.As(err, &ce) && ce.Code == code
}

// maxStateValue bounds one UI state value: it is small UI state, not storage.
const maxStateValue = 4096

func (s *Service) GetTargetState(ctx context.Context, providerID, target string) (map[string]string, error) {
	m, err := s.store.TargetState(ctx, providerID, target)
	if err != nil {
		return nil, coded(CodeInternal, err)
	}
	return m, nil
}

func (s *Service) SetTargetState(ctx context.Context, providerID, target, key, value string) error {
	if key == "" || len(value) > maxStateValue {
		return coded(CodeBadRequest, fmt.Errorf("bad state entry %q (%d bytes)", key, len(value)))
	}
	if err := s.store.SetTargetState(ctx, providerID, target, key, value); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}

// ResetTargetStateKey removes one per-target UI state key across all
// targets; the e2e test API uses it to give every spec a clean page
// snapshot (P19).
func (s *Service) ResetTargetStateKey(ctx context.Context, key string) error {
	if err := s.store.DeleteTargetStateKey(ctx, key); err != nil {
		return coded(CodeInternal, err)
	}
	return nil
}

// Stats is a snapshot for leak checks and measurements (test API only).
func (s *Service) Stats() map[string]any {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	views := 0
	for _, n := range s.views.Owners() {
		views += n
	}
	out := map[string]any{
		"views":      views,
		"streams":    s.streams.Count(streams.KindLogs),
		"terminals":  s.streams.Count(streams.KindTerm),
		"forwards":   s.fwd.Len(),
		"fwd_conns":  s.fwd.Conns(),
		"goroutines": runtime.NumGoroutine(),
		"heap_inuse": ms.HeapInuse,
		"heap_alloc": ms.HeapAlloc,
		"sys":        ms.Sys,
	}
	s.sessMu.Lock()
	out["sessions"] = len(s.sessions)
	for _, e := range s.sessions {
		if st, ok := e.sess.(interface{ Stats() map[string]int }); ok {
			for k, v := range st.Stats() {
				if prev, ok := out[k].(int); ok {
					v += prev
				}
				out[k] = v
			}
		}
	}
	s.sessMu.Unlock()
	return out
}
