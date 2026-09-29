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
)

// End is the last frame of every stream.
type End struct {
	K string `json:"k"` // "end"
	// Reason: "done" (complete), "gone" (the session was closed: reopen),
	// "error" (Message says why).
	Reason  string `json:"reason"`
	Class   string `json:"class,omitempty"`
	Message string `json:"message,omitempty"`
}

// Classifier maps a stream error to an error class for the end frame.
type Classifier func(error) (class, message string)

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
		cl = func(err error) (string, string) { return "internal", err.Error() }
	}
	return &Handler{
		reg: reg, token: []byte(base64.RawURLEncoding.EncodeToString(raw[:])), classify: cl,
		allowOrigin: o.AllowOrigin, saveDir: o.SaveDir,
		writeTimeout: writeTimeout, nudge: nudgeDelay, beat: heartbeat, saveReadTimeout: 60 * time.Second,
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
	s, ctx, done, err := h.reg.connect(r.Context(), id)
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
		end.Class, end.Message = h.classify(runErr)
	}
	_ = out.Frame(end)
	out.close()
}
