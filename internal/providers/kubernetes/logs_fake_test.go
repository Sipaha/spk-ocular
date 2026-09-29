package kubernetes

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/spk/spk-ocular/internal/provider"
)

// fakeKubelet serves pods/log like the apiserver + kubelet do: per
// container instance a log "file" of CRI records; timestamps=true prefixes
// each logical line (not partial continuations) with RFC3339Nano; tailLines
// counts logical lines; sinceTime (seconds) filters records; follow streams
// appended records until the instance ends. It can break connections, rotate
// files and answer errors.
type fakeKubelet struct {
	mu        sync.Mutex
	changed   chan struct{} // closed and replaced on every change
	cut       chan struct{} // closed: break every open follow
	eof       chan struct{} // closed: end every open follow cleanly
	ctrs      map[string]*fakeCtr
	forbidden bool
	requests  []url.Values
	srv       *httptest.Server
}

type fakeCtr struct {
	insts []*fakeInst // the last one is current
}

type fakeInst struct {
	id    string
	recs  []fakeRec
	ended bool
}

type fakeRec struct {
	ts      time.Time
	text    string
	partial bool // no newline after it (a CRI "P" record)
}

func newFakeKubelet(t *testing.T) *fakeKubelet {
	t.Helper()
	k := &fakeKubelet{changed: make(chan struct{}), cut: make(chan struct{}), eof: make(chan struct{}), ctrs: map[string]*fakeCtr{}}
	k.srv = httptest.NewServer(http.HandlerFunc(k.serve))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *fakeKubelet) fetcher(t *testing.T) logFetcher {
	t.Helper()
	f, err := httpLogFetcher(&rest.Config{Host: k.srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (k *fakeKubelet) bumpLocked() {
	close(k.changed)
	k.changed = make(chan struct{})
}

// start begins a new instance of pod/ctr.
func (k *fakeKubelet) start(pod, ctr, id string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := pod + "/" + ctr
	c := k.ctrs[key]
	if c == nil {
		c = &fakeCtr{}
		k.ctrs[key] = c
	}
	c.insts = append(c.insts, &fakeInst{id: id})
	k.bumpLocked()
}

func (k *fakeKubelet) cur(pod, ctr string) *fakeInst {
	c := k.ctrs[pod+"/"+ctr]
	return c.insts[len(c.insts)-1]
}

// log appends records to the current instance.
func (k *fakeKubelet) log(pod, ctr string, recs ...fakeRec) {
	k.mu.Lock()
	defer k.mu.Unlock()
	in := k.cur(pod, ctr)
	in.recs = append(in.recs, recs...)
	k.bumpLocked()
}

// end ends the current instance (its follows finish cleanly).
func (k *fakeKubelet) end(pod, ctr string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.cur(pod, ctr).ended = true
	k.bumpLocked()
}

// rotate drops the first n records of the current instance.
func (k *fakeKubelet) rotate(pod, ctr string, n int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	in := k.cur(pod, ctr)
	in.recs = append([]fakeRec(nil), in.recs[n:]...)
	k.bumpLocked()
}

// breakAll aborts every open follow (a dropped connection).
func (k *fakeKubelet) breakAll() {
	k.mu.Lock()
	defer k.mu.Unlock()
	close(k.cut)
	k.cut = make(chan struct{})
}

// endAll ends every open follow cleanly while the containers keep running
// (an apiserver timeout, a proxy closing an idle stream).
func (k *fakeKubelet) endAll() {
	k.mu.Lock()
	defer k.mu.Unlock()
	close(k.eof)
	k.eof = make(chan struct{})
}

func (k *fakeKubelet) requestLog() []url.Values {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]url.Values(nil), k.requests...)
}

func writeStatus(w http.ResponseWriter, code int, reason metav1.StatusReason, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(metav1.Status{TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"}, Status: "Failure", Code: int32(code), Reason: reason, Message: msg}) //nolint:gosec
}

func (k *fakeKubelet) serve(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/namespaces/"), "/")
	if len(parts) != 4 || parts[1] != "pods" || parts[3] != "log" {
		http.NotFound(w, r)
		return
	}
	pod, q := parts[2], r.URL.Query()
	ctr := q.Get("container")
	k.mu.Lock()
	k.requests = append(k.requests, q)
	if k.forbidden {
		k.mu.Unlock()
		writeStatus(w, http.StatusForbidden, metav1.StatusReasonForbidden, fmt.Sprintf(`pods "%s" is forbidden: User "viewer" cannot get resource "pods/log"`, pod))
		return
	}
	c := k.ctrs[pod+"/"+ctr]
	if c == nil {
		k.mu.Unlock()
		writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest, fmt.Sprintf("container %s is not valid for pod %s", ctr, pod))
		return
	}
	var in *fakeInst
	switch {
	case q.Get("previous") == "true" && len(c.insts) < 2:
		k.mu.Unlock()
		writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest, fmt.Sprintf(`previous terminated container "%s" in pod "%s" not found`, ctr, pod))
		return
	case q.Get("previous") == "true":
		in = c.insts[len(c.insts)-2]
	case len(c.insts) == 0:
		k.mu.Unlock()
		writeStatus(w, http.StatusBadRequest, metav1.StatusReasonBadRequest, fmt.Sprintf(`container "%s" in pod "%s" is waiting to start: ContainerCreating`, ctr, pod))
		return
	default:
		in = c.insts[len(c.insts)-1]
	}
	k.mu.Unlock()

	var since time.Time
	if s := q.Get("sinceTime"); s != "" {
		since, _ = time.Parse(time.RFC3339, s)
	}
	tail, _ := strconv.Atoi(q.Get("tailLines"))
	follow := q.Get("follow") == "true"
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	fl := w.(http.Flusher)

	k.mu.Lock()
	recs := append([]fakeRec(nil), in.recs...)
	k.mu.Unlock()
	start := 0
	if q.Has("tailLines") {
		start = tailStart(recs, tail)
	}
	atLineStart := true
	write := func(rs []fakeRec) {
		for _, rec := range rs {
			if rec.ts.Before(since) {
				atLineStart = !rec.partial
				continue
			}
			if atLineStart {
				_, _ = fmt.Fprintf(w, "%s ", rec.ts.UTC().Format(time.RFC3339Nano))
			}
			_, _ = w.Write([]byte(rec.text))
			if !rec.partial {
				_, _ = w.Write([]byte("\n"))
			}
			atLineStart = !rec.partial
		}
		fl.Flush()
	}
	write(recs[start:])
	sent := len(recs)
	if !follow {
		return
	}
	for {
		k.mu.Lock()
		sent = min(sent, len(in.recs)) // tests rotate only between connections
		more := append([]fakeRec(nil), in.recs[sent:]...)
		sent = len(in.recs)
		ended := in.ended
		changed, cut, eof := k.changed, k.cut, k.eof
		k.mu.Unlock()
		write(more)
		if ended {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-cut:
			panic(http.ErrAbortHandler) // the client sees a broken stream
		case <-eof:
			return
		case <-changed:
		}
	}
}

// tailStart: index of the first record of the last n logical lines (a
// logical line starts where the previous record ended with a newline).
func tailStart(recs []fakeRec, n int) int {
	if n <= 0 {
		return len(recs)
	}
	count := 0
	for i := len(recs) - 1; i >= 0; i-- {
		if i == 0 || !recs[i-1].partial {
			count++
			if count == n {
				return i
			}
		}
	}
	return 0
}

// logRecorder collects what a source delivers.
type logRecorder struct {
	mu      sync.Mutex
	lines   []provider.LogLine
	states  []provider.LogState
	ready   bool
	sources []string
	changed chan struct{}
}

func newLogRecorder() *logRecorder { return &logRecorder{changed: make(chan struct{})} }

func (s *logRecorder) bumpLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *logRecorder) Source(_ int, key, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources = append(s.sources, key)
	s.bumpLocked()
	return nil
}

func (s *logRecorder) Lines(_ int, l []provider.LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lines = append(s.lines, l...)
	s.bumpLocked()
	return nil
}

func (s *logRecorder) State(_ int, st provider.LogState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states = append(s.states, st)
	s.bumpLocked()
	return nil
}

func (s *logRecorder) Ready() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ready = true
	s.bumpLocked()
	return nil
}

func (s *logRecorder) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.lines))
	for i, l := range s.lines {
		out[i] = l.Text
	}
	return out
}

func (s *logRecorder) lastState() provider.LogState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.states) == 0 {
		return provider.LogState{}
	}
	return s.states[len(s.states)-1]
}

func (s *logRecorder) hasState(state string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if st.State == state {
			return true
		}
	}
	return false
}

// await waits until cond holds (checked on every delivery).
func (s *logRecorder) await(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		s.mu.Lock()
		ch := s.changed
		s.mu.Unlock()
		if cond() {
			return
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("timed out waiting for %s; lines=%q states=%+v", what, s.texts(), s.states)
		}
	}
}
