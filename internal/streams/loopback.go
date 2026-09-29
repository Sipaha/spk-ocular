package streams

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

var errLoopbackClosed = errors.New("stream server closed")

// Loopback serves a Handler on 127.0.0.1:<random port> for the desktop
// webview (whose page is wails://localhost). It listens only after the UI
// first asks for its address (Base), until Close, and answers only requests
// whose Host is exactly 127.0.0.1:<port> (no DNS rebinding).
type Loopback struct {
	h *Handler

	mu     sync.Mutex
	srv    *http.Server
	cancel context.CancelFunc
	host   string
	base   string
	closed bool
}

func NewLoopback(h *Handler) *Loopback { return &Loopback{h: h} }

// Base is http://127.0.0.1:<port>/<token>; the first call starts the server.
func (l *Loopback) Base() (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return "", errLoopbackClosed
	}
	if l.srv != nil {
		return l.base, nil
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	l.host = ln.Addr().String()
	l.base = "http://" + l.host + "/" + l.h.Token()
	host := l.host
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	l.srv = &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != host {
				l.h.reject(w, http.StatusForbidden, "host")
				return
			}
			l.h.ServeHTTP(w, r)
		}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(slog.Default().Handler(), slog.LevelDebug),
	}
	srv := l.srv
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("stream server stopped", "err", err)
		}
	}()
	return l.base, nil
}

// Close stops the server and ends open streams; Base fails afterwards.
func (l *Loopback) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = true
	if l.srv == nil {
		return nil
	}
	l.cancel()
	err := l.srv.Close()
	l.srv = nil
	return err
}
