package kubernetes

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/spk/spk-ocular/internal/provider"
)

// background: the session's target is not the selected one (P18). Its idle
// caches are kept (switching back is instant); its credential plugin runs
// headless (the shim sees the hold file, internal/execshim); a request
// that cannot go on without a person — the plugin failed headless, or a
// 401 — tells the API once (lost), which closes the session.
type background struct {
	mu   sync.Mutex
	on   bool
	lost func()
	told bool // lost was called in this stay in the background
}

// SetBackground implements provider.Backgrounder.
func (s *session) SetBackground(on bool, lost func()) {
	s.bg.mu.Lock()
	s.bg.on, s.bg.lost, s.bg.told = on, lost, false
	s.bg.mu.Unlock()
	s.setHold(on)
	s.caches.setBackground(on)
}

// setHold creates or removes the hold file the shim looks at.
func (s *session) setHold(on bool) {
	if s.hold == "" {
		return
	}
	if !on {
		if err := os.Remove(s.hold); err != nil && !os.IsNotExist(err) {
			slog.Warn("cannot remove a background hold", "err", err)
		}
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.hold), 0o700); err == nil {
		err = os.WriteFile(s.hold, nil, 0o600)
		if err == nil {
			return
		}
	}
	slog.Warn("cannot hold a background session: its credential plugin may ask for a login", "target", s.target)
}

// authFailed is told of every failed request's class: unauthorized in the
// background is lost (once per stay; asynchronously — the API closes the
// session, which stops the very informer telling this).
func (s *session) authFailed(class provider.ErrorClass) {
	if class != provider.ClassUnauthorized {
		return
	}
	s.bg.mu.Lock()
	lost := s.bg.lost
	fire := s.bg.on && !s.bg.told && lost != nil
	if fire {
		s.bg.told = true
	}
	s.bg.mu.Unlock()
	if fire {
		go lost()
	}
}
