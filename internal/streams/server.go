package streams

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/spk/spk-ocular/internal/core"
)

// End is the last frame of every stream.
type End struct {
	K string `json:"k"` // "end"
	// Reason: "done" (complete), "gone" (the session was closed: reopen),
	// "error" (Message says why).
	Reason  string `json:"reason"`
	Class   string `json:"class,omitempty"`
	Message string `json:"message,omitempty"`
	// Why: the provider's own sentence by key (the page says it in its
	// language); Message is its English text.
	Why *core.Message `json:"why,omitempty"`
}

// Classifier maps a stream error to an error class for the end frame, and
// the provider's own sentence when it is one.
type Classifier func(error) (class, message string, why *core.Message)

// Handler serves /<token>/logs/<id> (and /<token>/save, see save.go)
// relative to where it is mounted. Every request must carry the token (32
// random bytes, compared in constant time); the token is never logged.
type Handler struct {
	reg      *Registry
	token    []byte
	classify Classifier
	// allowOrigin is the desktop page's origin (wails://localhost): its
	// fetches are cross-origin "simple" requests (the token is in the path,
	// no custom headers), so they need exactly this ACAO and no preflight.
	// Empty in browser mode (same origin).
	allowOrigin string
	saveDir     func() (string, error)
	// writer timings (tests shorten them)
	writeTimeout, nudge, beat time.Duration
	saveReadTimeout           time.Duration
	term                      termTimings
}

type HandlerOptions struct {
	AllowOrigin string
	Classify    Classifier
	// SaveDir is where POST <base>/save writes files (the downloads
	// directory); nil disables saving.
	SaveDir func() (string, error)
}

func NewHandler(reg *Registry, o HandlerOptions) *Handler {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	cl := o.Classify
	if cl == nil {
		cl = func(err error) (string, string, *core.Message) { return "internal", err.Error(), nil }
	}
	return &Handler{
		reg: reg, token: []byte(base64.RawURLEncoding.EncodeToString(raw[:])), classify: cl,
		allowOrigin: o.AllowOrigin, saveDir: o.SaveDir,
		writeTimeout: writeTimeout, nudge: nudgeDelay, beat: heartbeat, saveReadTimeout: 60 * time.Second,
		term: defaultTermTimings,
	}
}

// Token is the path prefix the UI's base URL must carry.
func (h *Handler) Token() string { return string(h.token) }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Cache-Control", "no-store") // neither the tokenized URL nor the logs go to a cache
	if h.allowOrigin != "" {
		hd.Set("Access-Control-Allow-Origin", h.allowOrigin)
		hd.Set("Vary", "Origin")
	}
	token, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if !ok || subtle.ConstantTimeCompare([]byte(token), h.token) != 1 {
		h.reject(w, http.StatusNotFound, "path")
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != h.allowOrigin && !sameOrigin(o, r) {
		h.reject(w, http.StatusForbidden, "origin")
		return
	}
	switch {
	case strings.HasPrefix(rest, "logs/") && r.Method == http.MethodGet:
		h.serveStream(w, r, strings.TrimPrefix(rest, "logs/"))
	case strings.HasPrefix(rest, "term/") && r.Method == http.MethodGet:
		// Browsers always send Origin with a WebSocket handshake; without
		// it the request is not from our page (cross-site WebSocket
		// hijacking is exactly a foreign page opening this socket).
		if r.Header.Get("Origin") == "" {
			h.reject(w, http.StatusForbidden, "origin")
			return
		}
		if !isWebSocketUpgrade(r) {
			h.reject(w, http.StatusBadRequest, "upgrade")
			return
		}
		h.serveTerm(w, r, strings.TrimPrefix(rest, "term/"))
	case rest == "save" && r.Method == http.MethodPost && h.saveDir != nil:
		// A cross-site page can send a "simple" POST; CORS would only hide
		// the answer. The side effect needs an allowed Origin.
		if r.Header.Get("Origin") == "" {
			h.reject(w, http.StatusForbidden, "origin")
			return
		}
		h.serveSave(w, r)
	default:
		h.reject(w, http.StatusNotFound, "route")
	}
}

// sameOrigin: browser mode's page talks to its own server.
func sameOrigin(origin string, r *http.Request) bool {
	return origin == "http://"+r.Host
}

func (h *Handler) reject(w http.ResponseWriter, status int, reason string) {
	slog.Debug("stream request rejected", "status", status, "reason", reason) // no path: it carries the token
	http.Error(w, http.StatusText(status), status)
}

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, id string) {
	s, ctx, done, err := h.reg.connect(r.Context(), id, KindLogs)
	if err != nil {
		h.reject(w, http.StatusGone, "stream")
		return
	}
	defer done()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // a long-lived stream; the Writer bounds each write
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush() // headers now: the page sees the stream opened

	out := newWriter(w, rc.Flush, rc.SetWriteDeadline)
	out.timeout, out.nudge, out.beat = h.writeTimeout, h.nudge, h.beat
	runErr := s.run(ctx, out)
	end := End{K: "end", Reason: "done"}
	switch {
	case h.reg.wasRevoked(s):
		end.Reason = "gone"
	case r.Context().Err() != nil:
		out.close() // the page went away: nobody to tell
		return
	case runErr != nil && !errors.Is(runErr, context.Canceled):
		end.Reason = "error"
		end.Class, end.Message, end.Why = h.classify(runErr)
	}
	_ = out.Frame(end)
	out.close()
}

// isWebSocketUpgrade checks the handshake before the stream id is used, so
// a malformed request cannot burn it.
func isWebSocketUpgrade(r *http.Request) bool {
	if !headerHasToken(r.Header, "Connection", "upgrade") || !headerHasToken(r.Header, "Upgrade", "websocket") {
		return false
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(r.Header.Get("Sec-WebSocket-Key"))
	return err == nil && len(key) == 16
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for t := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

func (h *Handler) serveTerm(w http.ResponseWriter, r *http.Request, id string) {
	s, ctx, done, err := h.reg.connect(r.Context(), id, KindTerm)
	if err != nil {
		h.reject(w, http.StatusGone, "stream")
		return
	}
	defer done() // also closes the session (it ran or failed to start)
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
	// Origin was checked above (exactly ours); the library's own check
	// knows nothing about wails://.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Debug("terminal upgrade failed", "err", err)
		return
	}
	b := newTermBridge(conn, h.term, h.classify, s.size)
	b.run(ctx, s.term, func() bool { return h.reg.wasRevoked(s) })
}
