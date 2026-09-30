// Package enginefake is a fake Docker Engine for tests: an HTTP server on
// a unix socket (or loopback TCP, optionally TLS) that serves enough of the
// Engine API from an in-memory model — ping, info, container/network/
// volume/image lists and inspects, events (pushed by the test, the stream
// held open) and logs (bytes given by the test, optionally followed) — and
// lets a test count requests "on the wire", fail, stall or hang up on them.
package enginefake

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// DefaultAPIVersion is what the fake reports unless WithAPIVersion.
const DefaultAPIVersion = "1.54"

// Hook sees every request first (after it is counted). path is the
// request's path without the /vX.Y prefix. Returning true means the hook
// answered it.
type Hook func(w http.ResponseWriter, r *http.Request, path string) bool

// Request is one request as the fake received it.
type Request struct {
	Method  string
	Path    string // without the version prefix
	Version string // "" for an unversioned path
	Query   url.Values
	// RequestURI as on the request line (absolute when it came through a
	// proxy), Header and TransferEncoding as received.
	RequestURI       string
	Header           http.Header
	TransferEncoding []string
	ContentLength    int64
}

// Engine is a running fake. Its model is changed by the test at any time;
// every method is safe for concurrent use.
type Engine struct {
	srv    *httptest.Server
	host   string
	closed chan struct{}

	mu         sync.Mutex
	apiVersion string
	info       engine.Info
	containers map[string][]byte // id → inspect JSON
	networks   map[string][]byte
	volumes    map[string][]byte // name → inspect JSON
	images     map[string][]byte
	logs       map[string]*logState
	history    []engine.Event
	historyMax int
	subs       map[*subscriber]struct{}
	hooks      []Hook
	counts     map[string]int
	requests   []Request
}

type logState struct {
	data      []byte
	followers map[chan []byte]logOpts
	ended     bool
	// journal: the logs are Journal lines, served like the json-file
	// driver (since, tail, streams, timestamps applied) instead of data.
	journal bool
	entries []logEntry
}

type logEntry struct {
	stream byte
	at     time.Time
	text   string
}

// logOpts: what a logs request asked for.
type logOpts struct {
	stdout, stderr, stamps, tty bool
	since                       time.Time
	tail                        int // < 0: all
}

func (o logOpts) wants(e logEntry) bool {
	if e.stream == 2 && !o.tty {
		return o.stderr
	}
	return o.stdout || o.tty && o.stderr
}

func (o logOpts) format(e logEntry) []byte {
	text := e.text + "\n"
	if o.stamps {
		text = e.at.UTC().Format(time.RFC3339Nano) + " " + text
	}
	if o.tty {
		return []byte(text)
	}
	return Frame(e.stream, []byte(text))
}

type subscriber struct {
	ch      chan []byte
	filters engine.Filters
	end     chan struct{}
}

// Option configures New.
type Option func(*options)

type options struct {
	apiVersion string
	tcp        bool
	tls        *tls.Config
}

// WithAPIVersion sets the API version the fake reports and accepts.
func WithAPIVersion(v string) Option { return func(o *options) { o.apiVersion = v } }

// WithTCP listens on 127.0.0.1 instead of a unix socket; cfg non-nil
// serves TLS with it (its Certificates, else httptest's own certificate).
func WithTCP(cfg *tls.Config) Option {
	return func(o *options) { o.tcp, o.tls = true, cfg }
}

// New starts a fake Engine, stopped by t's cleanup. The unix socket lives
// in a fresh short directory under TMPDIR (sun_path is 108 bytes).
func New(t testing.TB, opts ...Option) *Engine {
	t.Helper()
	o := options{apiVersion: DefaultAPIVersion}
	for _, f := range opts {
		f(&o)
	}
	e := &Engine{
		closed:     make(chan struct{}),
		apiVersion: o.apiVersion,
		info: engine.Info{
			ID: "FAKE:ENGINE", Name: "fake-engine", ServerVersion: "29.0.0-fake",
			OSType: "linux", OperatingSystem: "Fake Linux", Architecture: "x86_64",
		},
		containers: map[string][]byte{},
		networks:   map[string][]byte{},
		volumes:    map[string][]byte{},
		images:     map[string][]byte{},
		logs:       map[string]*logState{},
		historyMax: 256,
		subs:       map[*subscriber]struct{}{},
		counts:     map[string]int{},
	}
	e.srv = httptest.NewUnstartedServer(http.HandlerFunc(e.serve))
	if o.tcp {
		e.host = "tcp://" + e.srv.Listener.Addr().String()
		if o.tls != nil {
			e.srv.TLS = o.tls
			e.srv.StartTLS()
		} else {
			e.srv.Start()
		}
	} else {
		dir, err := os.MkdirTemp("", "ef")
		if err != nil {
			t.Fatalf("enginefake: %v", err)
		}
		sock := filepath.Join(dir, "d.sock")
		if len(sock) >= 104 {
			_ = os.RemoveAll(dir)
			t.Fatalf("enginefake: socket path %q is too long for sun_path; use a shorter TMPDIR", sock)
		}
		l, err := net.Listen("unix", sock)
		if err != nil {
			_ = os.RemoveAll(dir)
			t.Fatalf("enginefake: %v", err)
		}
		_ = e.srv.Listener.Close()
		e.srv.Listener = l
		e.srv.Start()
		e.host = "unix://" + sock
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
	}
	t.Cleanup(e.Close)
	return e
}

// Host is the endpoint for engine.Config.Host.
func (e *Engine) Host() string { return e.host }

// Certificate is httptest's own TLS certificate (WithTCP with a config
// without Certificates), nil otherwise.
func (e *Engine) Certificate() *x509.Certificate { return e.srv.Certificate() }

// Close ends every held stream and stops the server.
func (e *Engine) Close() {
	e.mu.Lock()
	select {
	case <-e.closed:
		e.mu.Unlock()
		return
	default:
	}
	close(e.closed)
	e.mu.Unlock()
	e.srv.CloseClientConnections()
	e.srv.Close()
}

// SetAPIVersion changes the reported and accepted API version.
func (e *Engine) SetAPIVersion(v string) {
	e.mu.Lock()
	e.apiVersion = v
	e.mu.Unlock()
}

// SetInfo replaces the /info answer (SystemTime zero: the fake's clock).
func (e *Engine) SetInfo(i engine.Info) {
	e.mu.Lock()
	e.info = i
	e.mu.Unlock()
}

// AddHook adds a hook after the existing ones; ClearHooks removes all.
func (e *Engine) AddHook(h Hook) {
	e.mu.Lock()
	e.hooks = append(e.hooks, h)
	e.mu.Unlock()
}

func (e *Engine) ClearHooks() {
	e.mu.Lock()
	e.hooks = nil
	e.mu.Unlock()
}

// Count is how many requests for path (without the version prefix)
// reached the fake.
func (e *Engine) Count(path string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.counts[path]
}

// Requests are all requests received so far, in order.
func (e *Engine) Requests() []Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Request(nil), e.requests...)
}

// ---- model ----

// PutContainer adds or replaces a container (by ID).
func (e *Engine) PutContainer(c engine.ContainerInspect) { e.PutContainerJSON(c.ID, mustJSON(c)) }

// PutContainerJSON stores an inspect answer as is (fields the client does
// not decode included).
func (e *Engine) PutContainerJSON(id string, raw []byte) {
	e.mu.Lock()
	e.containers[id] = raw
	e.mu.Unlock()
}

func (e *Engine) RemoveContainer(id string) {
	e.mu.Lock()
	delete(e.containers, id)
	e.mu.Unlock()
}

func (e *Engine) PutNetwork(n engine.Network) { e.PutNetworkJSON(n.ID, mustJSON(n)) }

func (e *Engine) PutNetworkJSON(id string, raw []byte) {
	e.mu.Lock()
	e.networks[id] = raw
	e.mu.Unlock()
}

func (e *Engine) RemoveNetwork(id string) {
	e.mu.Lock()
	delete(e.networks, id)
	e.mu.Unlock()
}

func (e *Engine) PutVolume(v engine.Volume) { e.PutVolumeJSON(v.Name, mustJSON(v)) }

func (e *Engine) PutVolumeJSON(name string, raw []byte) {
	e.mu.Lock()
	e.volumes[name] = raw
	e.mu.Unlock()
}

func (e *Engine) RemoveVolume(name string) {
	e.mu.Lock()
	delete(e.volumes, name)
	e.mu.Unlock()
}

func (e *Engine) PutImage(i engine.ImageInspect) { e.PutImageJSON(i.ID, mustJSON(i)) }

func (e *Engine) PutImageJSON(id string, raw []byte) {
	e.mu.Lock()
	e.images[id] = raw
	e.mu.Unlock()
}

func (e *Engine) RemoveImage(id string) {
	e.mu.Lock()
	delete(e.images, id)
	e.mu.Unlock()
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---- events ----

// Emit records an event in the history (the last 256, like moby) and
// sends it to every open events stream whose filters match. TimeNano 0
// takes the current time. A subscriber that does not read is skipped
// (like moby's slow subscriber), the stream stays open.
func (e *Engine) Emit(ev engine.Event) {
	if ev.TimeNano == 0 {
		now := time.Now()
		ev.Time, ev.TimeNano = now.Unix(), now.UnixNano()
	}
	line := append(mustJSON(ev), '\n')
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = append(e.history, ev)
	if len(e.history) > e.historyMax {
		e.history = e.history[len(e.history)-e.historyMax:]
	}
	for s := range e.subs {
		if eventMatches(ev, s.filters) {
			select {
			case s.ch <- line:
			default:
			}
		}
	}
}

// EmitRaw sends bytes as they are to every open events stream (malformed
// or oversized events); nothing is recorded.
func (e *Engine) EmitRaw(b []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for s := range e.subs {
		select {
		case s.ch <- b:
		default:
		}
	}
}

// EndEvents ends every open events stream (a clean EOF) after what was
// emitted to it.
func (e *Engine) EndEvents() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for s := range e.subs {
		close(s.end)
		delete(e.subs, s)
	}
}

// EventSubscribers is the number of open events streams.
func (e *Engine) EventSubscribers() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.subs)
}

// WaitEventSubscribers waits until n events streams are open.
func (e *Engine) WaitEventSubscribers(t testing.TB, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.EventSubscribers() != n {
		if time.Now().After(deadline) {
			t.Fatalf("enginefake: %d events streams, want %d", e.EventSubscribers(), n)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ---- logs ----

// SetLogs sets what a container's logs answer serves (bytes as the Engine
// sends them: stdcopy frames — see Frame — or a TTY's raw output). The
// fake does not apply since/tail/timestamps; Requests shows what was asked.
func (e *Engine) SetLogs(id string, data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.logState(id)
	st.data = append([]byte(nil), data...)
	st.ended = false
}

// AppendLogs adds bytes to the served logs and sends them to followers.
func (e *Engine) AppendLogs(id string, data []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.logState(id)
	st.data = append(st.data, data...)
	for ch := range st.followers {
		select {
		case ch <- append([]byte(nil), data...):
		default:
			panic("enginefake: a logs follower is 1024 chunks behind")
		}
	}
}

// Journal adds a line to the container's log journal (stream 1 stdout, 2
// stderr) and sends it to the followers that asked for its stream. A
// container with a journal is served from it (see logState.journal).
func (e *Engine) Journal(id string, stream byte, at time.Time, text string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.logState(id)
	st.journal = true
	ent := logEntry{stream: stream, at: at, text: text}
	st.entries = append(st.entries, ent)
	for ch, o := range st.followers {
		if !o.wants(ent) {
			continue
		}
		select {
		case ch <- o.format(ent):
		default:
			panic("enginefake: a logs follower is 1024 chunks behind")
		}
	}
}

// RotateJournal drops the journal lines before t (a rotated log file).
func (e *Engine) RotateJournal(id string, t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.logState(id)
	var keep []logEntry
	for _, ent := range st.entries {
		if !ent.at.Before(t) {
			keep = append(keep, ent)
		}
	}
	st.entries = keep
}

// ResumeLogs lets follows of the container follow again (it started).
func (e *Engine) ResumeLogs(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logState(id).ended = false
}

// EndLogs ends the container's followed logs streams (EOF) and makes later
// follows end after the data too.
func (e *Engine) EndLogs(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.logState(id)
	st.ended = true
	for ch := range st.followers {
		close(ch)
		delete(st.followers, ch)
	}
}

// LogFollowers is the number of open followed logs streams of a container.
func (e *Engine) LogFollowers(id string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.logState(id).followers)
}

func (e *Engine) logState(id string) *logState {
	st := e.logs[id]
	if st == nil {
		st = &logState{followers: map[chan []byte]logOpts{}}
		e.logs[id] = st
	}
	return st
}

// Frame is one stdcopy frame: stream 1 stdout, 2 stderr, 3 a daemon error.
func Frame(stream byte, payload []byte) []byte {
	b := make([]byte, 8, 8+len(payload))
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(payload))) //nolint:gosec // test data
	return append(b, payload...)
}

// ---- hooks ----

// Fail answers requests for path with status and an Engine error body.
func Fail(path string, status int, message string) Hook {
	return func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != path {
			return false
		}
		writeError(w, status, message)
		return true
	}
}

// StallHeaders holds requests for path before any answer until release is
// closed (then lets them through), the client goes away or the fake closes.
func StallHeaders(path string, release <-chan struct{}) Hook {
	return func(_ http.ResponseWriter, r *http.Request, p string) bool {
		if p != path {
			return false
		}
		select {
		case <-release:
			return false
		case <-r.Context().Done():
			return true
		}
	}
}

// StallBody answers requests for path with 200, the given headers and the
// first bytes of a body, then holds the rest until the client goes away.
func StallBody(path string, header http.Header, first []byte) Hook {
	return func(w http.ResponseWriter, r *http.Request, p string) bool {
		if p != path {
			return false
		}
		for k, v := range header {
			w.Header()[k] = v
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(first)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	}
}

// HangUp closes the connection of requests for path after reading them,
// without an answer.
func HangUp(path string) Hook {
	return func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != path {
			return false
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return true
	}
}

// Redirect answers requests for path with a redirect to location.
func Redirect(path, location string) Hook {
	return func(w http.ResponseWriter, r *http.Request, p string) bool {
		if p != path {
			return false
		}
		http.Redirect(w, r, location, http.StatusFound)
		return true
	}
}

// Body answers requests for path with 200 and body (e.g. an oversized one).
func Body(path string, body []byte) Hook {
	return func(w http.ResponseWriter, _ *http.Request, p string) bool {
		if p != path {
			return false
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
		return true
	}
}

// ---- serving ----

var versioned = regexp.MustCompile(`^/v(\d+\.\d+)(/.*)$`)

func (e *Engine) serve(w http.ResponseWriter, r *http.Request) {
	path, ver := r.URL.Path, ""
	if m := versioned.FindStringSubmatch(path); m != nil {
		ver, path = m[1], m[2]
	}
	e.mu.Lock()
	e.counts[path]++
	e.requests = append(e.requests, Request{
		Method: r.Method, Path: path, Version: ver, Query: r.URL.Query(),
		RequestURI: r.RequestURI, Header: r.Header.Clone(),
		TransferEncoding: r.TransferEncoding, ContentLength: r.ContentLength,
	})
	hooks := append([]Hook(nil), e.hooks...)
	apiVersion := e.apiVersion
	e.mu.Unlock()
	for _, h := range hooks {
		if h(w, r, path) {
			return
		}
	}
	if ver != "" && versionLess(apiVersion, ver) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("client version %s is too new. Maximum supported API version is %s", ver, apiVersion))
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "the fake Engine is read-only")
		return
	}
	switch {
	case path == "/_ping":
		w.Header().Set("Api-Version", apiVersion)
		w.Header().Set("OSType", "linux")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		_, _ = w.Write([]byte("OK"))
	case path == "/info":
		e.mu.Lock()
		info := e.info
		e.mu.Unlock()
		if info.SystemTime.IsZero() {
			info.SystemTime = engine.TimeOf(time.Now())
		}
		writeJSON(w, info)
	case path == "/containers/json":
		e.list(w, r, e.containers, containerFilters, func(raw []byte) any { return containerSummary(raw) })
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
		e.inspect(w, e.containers, strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json"), "container", containerName)
	case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/logs"):
		e.serveLogs(w, r, strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/logs"))
	case path == "/networks":
		e.list(w, r, e.networks, networkFilters, func(raw []byte) any { return networkSummary(raw) })
	case strings.HasPrefix(path, "/networks/"):
		e.inspect(w, e.networks, strings.TrimPrefix(path, "/networks/"), "network", nameField)
	case path == "/volumes":
		e.listVolumes(w, r)
	case strings.HasPrefix(path, "/volumes/"):
		e.inspect(w, e.volumes, strings.TrimPrefix(path, "/volumes/"), "volume", nil)
	case path == "/images/json":
		e.list(w, r, e.images, imageFilters, func(raw []byte) any { return imageSummary(raw) })
	case strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json"):
		e.inspect(w, e.images, strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json"), "image", imageTagged)
	case path == "/events":
		e.serveEvents(w, r)
	default:
		writeError(w, http.StatusNotFound, "page not found")
	}
}

func versionLess(a, b string) bool {
	var am, an, bm, bn int
	_, _ = fmt.Sscanf(a, "%d.%d", &am, &an)
	_, _ = fmt.Sscanf(b, "%d.%d", &bm, &bn)
	return am < bm || am == bm && an < bn
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": message})
}

// filterKeys: the filters a list supports, and how an object matches one.
type filterFunc func(raw []byte, value string) bool

func (e *Engine) list(w http.ResponseWriter, r *http.Request, m map[string][]byte, keys map[string]filterFunc, summary func([]byte) any) {
	f, ok := parseFilters(w, r, keys)
	if !ok {
		return
	}
	e.mu.Lock()
	ids := sortedKeys(m)
	raws := make([][]byte, 0, len(ids))
	for _, id := range ids {
		raws = append(raws, m[id])
	}
	e.mu.Unlock()
	out := []any{}
	for _, raw := range raws {
		if matches(raw, f, keys) {
			out = append(out, summary(raw))
		}
	}
	writeJSON(w, out)
}

func (e *Engine) listVolumes(w http.ResponseWriter, r *http.Request) {
	f, ok := parseFilters(w, r, volumeFilters)
	if !ok {
		return
	}
	e.mu.Lock()
	var out []json.RawMessage
	for _, name := range sortedKeys(e.volumes) {
		if matches(e.volumes[name], f, volumeFilters) {
			out = append(out, e.volumes[name])
		}
	}
	e.mu.Unlock()
	writeJSON(w, map[string]any{"Volumes": out, "Warnings": nil})
}

// inspect finds an object by id, unique id prefix or name (like the Engine).
func (e *Engine) inspect(w http.ResponseWriter, m map[string][]byte, ref, kind string, name func([]byte) string) {
	e.mu.Lock()
	raw, ok := m[ref]
	if !ok {
		var found [][]byte
		for id, b := range m {
			if strings.HasPrefix(id, ref) || name != nil && slices.Contains(strings.Split(name(b), "\n"), ref) {
				found = append(found, b)
			}
		}
		if len(found) == 1 {
			raw, ok = found[0], true
		}
	}
	e.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("No such %s: %s", kind, ref))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (e *Engine) serveEvents(w http.ResponseWriter, r *http.Request) {
	f, ok := parseFilters(w, r, map[string]filterFunc{"type": nil, "event": nil, "label": nil, "container": nil, "network": nil, "volume": nil, "image": nil})
	if !ok {
		return
	}
	var since int64
	if s := r.URL.Query().Get("since"); s != "" {
		sec, frac, _ := strings.Cut(s, ".")
		var a, b int64
		_, _ = fmt.Sscanf(sec, "%d", &a)
		if frac != "" {
			frac = (frac + "000000000")[:9]
			_, _ = fmt.Sscanf(frac, "%d", &b)
		}
		since = a*int64(time.Second) + b
	}
	sub := &subscriber{ch: make(chan []byte, 1024), filters: f, end: make(chan struct{})}
	e.mu.Lock()
	var backlog [][]byte
	if since != 0 {
		for _, ev := range e.history {
			if ev.TimeNano >= since && eventMatches(ev, f) {
				backlog = append(backlog, append(mustJSON(ev), '\n'))
			}
		}
	}
	e.subs[sub] = struct{}{}
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.subs, sub)
		e.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fl := w.(http.Flusher)
	for _, b := range backlog {
		_, _ = w.Write(b)
	}
	fl.Flush()
	for {
		select {
		case b := <-sub.ch:
			if _, err := w.Write(b); err != nil {
				return
			}
			fl.Flush()
		case <-sub.end:
			for { // what was emitted before the end still goes out
				select {
				case b := <-sub.ch:
					_, _ = w.Write(b)
				default:
					fl.Flush()
					return
				}
			}
		case <-r.Context().Done():
			return
		case <-e.closed:
			return
		}
	}
}

func (e *Engine) serveLogs(w http.ResponseWriter, r *http.Request, id string) {
	q := r.URL.Query()
	on := func(k string) bool { return q.Get(k) == "1" || q.Get(k) == "true" }
	o := logOpts{stdout: on("stdout"), stderr: on("stderr"), stamps: on("timestamps"), tail: -1}
	if t := q.Get("tail"); t != "" && t != "all" {
		_, _ = fmt.Sscanf(t, "%d", &o.tail)
	}
	if sv := q.Get("since"); sv != "" {
		sec, frac, _ := strings.Cut(sv, ".")
		var a, b int64
		_, _ = fmt.Sscanf(sec, "%d", &a)
		if frac != "" {
			frac = (frac + "000000000")[:9]
			_, _ = fmt.Sscanf(frac, "%d", &b)
		}
		o.since = time.Unix(a, b)
	}
	e.mu.Lock()
	raw, known := e.containers[id]
	if known {
		o.tty = decode[engine.ContainerInspect](raw).Config.Tty
	}
	st := e.logState(id)
	var data []byte
	if st.journal {
		// since is positional, like the json-file reader: from the first
		// line stamped at or after it, everything after in journal order.
		var picked []logEntry
		from := o.since.IsZero()
		for _, ent := range st.entries {
			from = from || !ent.at.Before(o.since)
			if o.wants(ent) && from {
				picked = append(picked, ent)
			}
		}
		if o.tail >= 0 && len(picked) > o.tail {
			picked = picked[len(picked)-o.tail:]
		}
		for _, ent := range picked {
			data = append(data, o.format(ent)...)
		}
	} else {
		data = append([]byte(nil), st.data...)
	}
	follow := on("follow")
	var ch chan []byte
	if follow && !st.ended && known {
		ch = make(chan []byte, 1024)
		st.followers[ch] = o
	}
	e.mu.Unlock()
	if ch != nil {
		defer func() {
			e.mu.Lock()
			delete(st.followers, ch)
			e.mu.Unlock()
		}()
	}
	if !known {
		writeError(w, http.StatusNotFound, "No such container: "+id)
		return
	}
	if o.tty {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	} else {
		w.Header().Set("Content-Type", "application/vnd.docker.multiplexed-stream")
	}
	w.WriteHeader(http.StatusOK)
	fl := w.(http.Flusher)
	_, _ = w.Write(data)
	fl.Flush()
	if ch == nil {
		return
	}
	for {
		select {
		case b, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(b); err != nil {
				return
			}
			fl.Flush()
		case <-r.Context().Done():
			return
		case <-e.closed:
			return
		}
	}
}

func parseFilters(w http.ResponseWriter, r *http.Request, keys map[string]filterFunc) (engine.Filters, bool) {
	s := r.URL.Query().Get("filters")
	if s == "" {
		return nil, true
	}
	var f engine.Filters
	if err := json.Unmarshal([]byte(s), &f); err != nil {
		writeError(w, http.StatusBadRequest, "invalid filters: "+err.Error())
		return nil, false
	}
	for k := range f {
		if _, ok := keys[k]; !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid filter '%s'", k))
			return nil, false
		}
	}
	return f, true
}

// matches: every key matches one of its values (the Engine's semantics).
func matches(raw []byte, f engine.Filters, keys map[string]filterFunc) bool {
	for k, vals := range f {
		fn := keys[k]
		ok := false
		for _, v := range vals {
			if fn(raw, v) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func labelMatch(labels map[string]string, v string) bool {
	k, want, hasValue := strings.Cut(v, "=")
	got, ok := labels[k]
	return ok && (!hasValue || got == want)
}

func sortedKeys(m map[string][]byte) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func decode[T any](raw []byte) T {
	var v T
	_ = json.Unmarshal(raw, &v)
	return v
}

func containerName(raw []byte) string {
	return strings.TrimPrefix(decode[engine.ContainerInspect](raw).Name, "/")
}

// imageTagged: an image is found by any of its tags (the name argument
// of inspect compares with one string; a tag list is joined by '\n').
func imageTagged(raw []byte) string {
	return strings.Join(decode[engine.ImageInspect](raw).RepoTags, "\n")
}

func nameField(raw []byte) string {
	return decode[struct{ Name string }](raw).Name
}

var containerFilters = map[string]filterFunc{
	"label": func(raw []byte, v string) bool {
		return labelMatch(decode[engine.ContainerInspect](raw).Config.Labels, v)
	},
	"id":   func(raw []byte, v string) bool { return strings.HasPrefix(decode[engine.ContainerInspect](raw).ID, v) },
	"name": func(raw []byte, v string) bool { return strings.Contains(containerName(raw), v) },
	"status": func(raw []byte, v string) bool {
		return decode[engine.ContainerInspect](raw).State.Status == v
	},
}

var networkFilters = map[string]filterFunc{
	"label": func(raw []byte, v string) bool { return labelMatch(decode[engine.Network](raw).Labels, v) },
	"id":    func(raw []byte, v string) bool { return strings.HasPrefix(decode[engine.Network](raw).ID, v) },
	"name":  func(raw []byte, v string) bool { return strings.Contains(nameField(raw), v) },
}

var volumeFilters = map[string]filterFunc{
	"label": func(raw []byte, v string) bool { return labelMatch(decode[engine.Volume](raw).Labels, v) },
	"name":  func(raw []byte, v string) bool { return strings.Contains(nameField(raw), v) },
}

var imageFilters = map[string]filterFunc{
	"label": func(raw []byte, v string) bool { return labelMatch(decode[engine.ImageInspect](raw).Config.Labels, v) },
}

func eventMatches(ev engine.Event, f engine.Filters) bool {
	for k, vals := range f {
		ok := false
		for _, v := range vals {
			switch k {
			case "type":
				ok = ev.Type == v
			case "event": // like moby: "health_status: healthy" matches health_status
				action, _, _ := strings.Cut(ev.Action, ":")
				ok = ev.Action == v || action == v
			case "label":
				ok = labelMatch(ev.Actor.Attributes, v)
			case "container", "network", "volume", "image":
				ok = ev.Type == k && (ev.Actor.ID == v || ev.Actor.Attributes["name"] == v)
			}
			if ok {
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func containerSummary(raw []byte) engine.ContainerSummary {
	c := decode[engine.ContainerInspect](raw)
	status := c.State.Status
	switch {
	case c.State.Running:
		status = "Up"
	case c.State.Status == "exited":
		status = fmt.Sprintf("Exited (%d)", c.State.ExitCode)
	}
	return engine.ContainerSummary{
		ID: c.ID, Names: []string{"/" + strings.TrimPrefix(c.Name, "/")},
		Image: c.Config.Image, ImageID: c.Image, Created: c.Created.Unix(),
		State: c.State.Status, Status: status, Labels: c.Config.Labels,
	}
}

// networkSummary: a list item has no Containers (like the Engine).
func networkSummary(raw []byte) engine.Network {
	n := decode[engine.Network](raw)
	n.Containers = nil
	return n
}

func imageSummary(raw []byte) engine.ImageSummary {
	i := decode[engine.ImageInspect](raw)
	return engine.ImageSummary{
		ID: i.ID, RepoTags: i.RepoTags, RepoDigests: i.RepoDigests,
		Created: i.Created.Unix(), Size: i.Size, Labels: i.Config.Labels,
	}
}
