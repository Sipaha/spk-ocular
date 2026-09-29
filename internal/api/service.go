package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/events"
	"github.com/spk/spk-ocular/internal/provider"
	"github.com/spk/spk-ocular/internal/store"
	"github.com/spk/spk-ocular/internal/streams"
	"github.com/spk/spk-ocular/internal/views"
)

const prefSelectedTarget = "selected_target"

type Options struct {
	Version string
	Mode    string // "desktop" | "browser"
	Getenv  func(string) string
}

// Service implements API.
type Service struct {
	reg   *provider.Registry
	store *store.Store
	em    *events.Emitter
	opts  Options

	views      *views.Manager
	streams    *streams.Registry
	streamBase func() (string, error)

	sessMu   sync.Mutex
	sessions map[string]*sessionEntry // by ownerKey
	reaper   *time.Timer
	sessSeq  uint64
	now      func() time.Time

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

var _ API = (*Service)(nil)

func NewService(reg *provider.Registry, st *store.Store, em *events.Emitter, o Options) *Service {
	return &Service{reg: reg, store: st, em: em, opts: o, views: views.NewManager(em), streams: streams.NewRegistry(), sessions: map[string]*sessionEntry{}, now: time.Now}
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
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.views.CloseAll()
	s.streams.Close()
	s.sessMu.Lock()
	if s.reaper != nil {
		s.reaper.Stop()
	}
	for key := range s.sessions {
		s.closeSessionLocked(key)
	}
	s.sessMu.Unlock()
}

func (s *Service) AppInfo(context.Context) (AppInfo, error) {
	return AppInfo{
		Name:     "SPK Ocular",
		Version:  s.opts.Version,
		Mode:     s.opts.Mode,
		Language: uiLanguage(s.opts.Getenv),
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
		d, err := p.Discover(ctx)
		if err != nil {
			g.Error = err.Error()
		} else {
			g.Targets = append(g.Targets, d.Targets...)
			g.Problems = append(g.Problems, d.Problems...)
		}
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
	b, _ := json.Marshal(TargetRef{Provider: providerID, ID: id})
	if err := s.store.SetUIPref(ctx, prefSelectedTarget, string(b)); err != nil {
		return coded(CodeInternal, err)
	}
	s.closeOtherSessions(ownerKey(providerID, id))
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

// uiLanguage follows the system's message language (gettext order:
// LANGUAGE, LC_ALL, LC_MESSAGES, LANG). Russian or English.
func uiLanguage(getenv func(string) string) string {
	if getenv == nil {
		return "en"
	}
	for _, k := range []string{"LANGUAGE", "LC_ALL", "LC_MESSAGES", "LANG"} {
		v := getenv(k)
		if v == "" {
			continue
		}
		first, _, _ := strings.Cut(v, ":") // LANGUAGE is a list
		if strings.HasPrefix(first, "ru") {
			return "ru"
		}
		return "en" // C/POSIX included: gettext shows untranslated messages
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
