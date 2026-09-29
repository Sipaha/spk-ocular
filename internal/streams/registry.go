// Package streams serves long-lived streams to the UI: container logs
// (NDJSON over a GET) and terminals (a WebSocket). In desktop mode they come
// from a token-protected loopback server (never wails://: WebKitGTK buffers
// and truncates streams there), in browser mode from the main server under
// /streams/. A stream is opened in two steps: the API validates the request
// and registers it under a single-use id (Registry.Add / AddTerm), then the
// page connects to <base>/logs/<id> or <base>/term/<id> and the stream runs
// until it ends, the page goes away, or its owner is closed (a provider
// session incarnation for logs; terminals belong to the app).
package streams

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/provider"
)

// Func runs one stream: it writes frames to out until ctx ends (the page
// went away, the owner closed) or the stream is complete. Its error becomes
// the final "end" frame.
type Func func(ctx context.Context, out *Writer) error

// TermSession is a prepared terminal (provider.ExecHandle satisfies it).
// Close releases it whether or not it ran.
type TermSession interface {
	Run(ctx context.Context, t provider.Terminal) (provider.ExitStatus, error)
	Close()
}

// Kind of a stream: the route it is served on and its own limit.
type Kind string

const (
	KindLogs Kind = "logs"
	KindTerm Kind = "term"
)

const (
	// connectTTL: a registered stream the page did not connect to within
	// this long is dropped (the page went away between the two steps).
	connectTTL = 30 * time.Second
	// MaxStreams bounds registered + connected log streams of the whole app.
	MaxStreams = 8
	// MaxTerms bounds registered + connected terminals.
	MaxTerms = 16
	// closeWait bounds how long Close waits for connected streams to end.
	closeWait = 5 * time.Second
)

var limits = map[Kind]int{KindLogs: MaxStreams, KindTerm: MaxTerms}

var (
	// ErrGone: unknown, already used, expired or revoked stream id / owner.
	ErrGone = errors.New("stream is gone")
	// ErrLimit: too many open streams of a kind.
	ErrLimit = errors.New("too many open streams")
)

type stream struct {
	kind      Kind
	owner     string
	run       Func        // logs
	term      TermSession // terminals
	size      provider.TermSize
	created   time.Time
	connected bool
	cancel    context.CancelFunc // set on connect
	gone      bool               // owner closed: the end frame says so
}

// release frees what a stream holds when it is dropped without running.
func (s *stream) release() {
	if s.term != nil {
		s.term.Close()
	}
}

// Registry holds registered and connected streams. Ids are opaque and never
// reused (random epoch + counter).
type Registry struct {
	epoch string
	now   func() time.Time

	mu      sync.Mutex
	seq     uint64
	streams map[string]*stream
	closed  bool
	running sync.WaitGroup // connected streams
}

func NewRegistry() *Registry {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Registry{epoch: hex.EncodeToString(b), now: time.Now, streams: map[string]*stream{}}
}

// Add registers a log stream run for owner and returns the id the page
// connects with. The caller must make sure owner is alive at this moment
// (the API calls it under the lock that also guards closing the owner), so
// a stream can never outlive a closed owner.
func (r *Registry) Add(owner string, run Func) (string, error) {
	id, release, err := r.Register(owner, run)
	release()
	return id, err
}

// Register is Add for a caller holding its own lock: release closes the
// streams that expired meanwhile (terminal handles among them) and runs
// once the caller holds no lock.
func (r *Registry) Register(owner string, run Func) (id string, release func(), err error) {
	return r.add(&stream{kind: KindLogs, owner: owner, run: run})
}

// AddTerm registers a run of a terminal, starting at size. owner names the
// terminal, not a session (terminals outlive sessions): CloseOwner(owner)
// ends its runs when its tab is gone. The registry owns t from now on: it
// is closed if the page never connects, the app closes, or it has run. On
// error t is closed too.
func (r *Registry) AddTerm(owner string, t TermSession, size provider.TermSize) (string, error) {
	id, release, err := r.RegisterTerm(owner, t, size)
	release()
	return id, err
}

// RegisterTerm is AddTerm for a caller holding its own lock: what has to
// be closed (t when refused, streams that expired meanwhile) is closed by
// release, which the caller runs once it holds no lock (provider cleanup
// may block or call back).
func (r *Registry) RegisterTerm(owner string, t TermSession, size provider.TermSize) (id string, release func(), err error) {
	id, release, err = r.add(&stream{kind: KindTerm, owner: owner, term: t, size: size})
	if err != nil {
		expired := release
		release = func() {
			expired()
			t.Close()
		}
	}
	return id, release, err
}

// add registers s; release closes the expired streams it dropped.
func (r *Registry) add(s *stream) (string, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", func() {}, ErrGone
	}
	dropped := r.dropExpiredLocked()
	release := func() { releaseAll(dropped) }
	n := 0
	for _, o := range r.streams {
		if o.kind == s.kind {
			n++
		}
	}
	if n >= limits[s.kind] {
		return "", release, fmt.Errorf("%w (max %d %s)", ErrLimit, limits[s.kind], s.kind)
	}
	r.seq++
	id := fmt.Sprintf("s%s-%d", r.epoch, r.seq)
	s.created = r.now()
	r.streams[id] = s
	return id, release, nil
}

func releaseAll(ss []*stream) {
	for _, s := range ss {
		s.release()
	}
}

// dropExpiredLocked removes registered streams nobody connected to in time;
// the caller releases them outside the lock.
func (r *Registry) dropExpiredLocked() []*stream {
	var out []*stream
	for id, s := range r.streams {
		if !s.connected && r.now().Sub(s.created) > connectTTL {
			delete(r.streams, id)
			out = append(out, s)
		}
	}
	return out
}

// connect takes a registered stream of kind (once) and gives it a context
// that the owner's closing cancels. done must be called when the stream has
// ended. An id of another kind is not consumed.
func (r *Registry) connect(parent context.Context, id string, kind Kind) (s *stream, ctx context.Context, done func(), err error) {
	var dropped []*stream
	defer func() { releaseAll(dropped) }()
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped = r.dropExpiredLocked()
	s = r.streams[id]
	if r.closed || s == nil || s.connected || s.kind != kind {
		return nil, nil, nil, ErrGone
	}
	s.connected = true
	ctx, s.cancel = context.WithCancel(parent)
	cancel := s.cancel
	r.running.Add(1)
	return s, ctx, func() {
		cancel()
		s.release()
		r.mu.Lock()
		delete(r.streams, id)
		r.mu.Unlock()
		r.running.Done()
	}, nil
}

// wasRevoked reports whether s ended because its owner was closed.
func (r *Registry) wasRevoked(s *stream) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return s.gone
}

// CloseOwner ends every stream of owner (its session went away): connected
// ones finish with an end frame "gone", registered ones can no longer
// connect.
func (r *Registry) CloseOwner(owner string) {
	var dropped []*stream
	defer func() { releaseAll(dropped) }()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, s := range r.streams {
		if s.owner != owner {
			continue
		}
		s.gone = true
		if s.connected {
			s.cancel()
		} else {
			delete(r.streams, id)
			dropped = append(dropped, s)
		}
	}
}

// Owners counts live (registered or connected) streams per owner: a session
// with an open log tab is in use even without table views.
func (r *Registry) Owners() map[string]int {
	var dropped []*stream
	defer func() { releaseAll(dropped) }()
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped = r.dropExpiredLocked()
	out := map[string]int{}
	for _, s := range r.streams {
		out[s.owner]++
	}
	return out
}

// Len is the number of live streams of every kind.
func (r *Registry) Len() int { return r.Count(KindLogs) + r.Count(KindTerm) }

// Count is the number of live streams of kind (for /api/_test/stats).
func (r *Registry) Count(kind Kind) int {
	var dropped []*stream
	defer func() { releaseAll(dropped) }()
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped = r.dropExpiredLocked()
	n := 0
	for _, s := range r.streams {
		if s.kind == kind {
			n++
		}
	}
	return n
}

// Close ends every stream (connected ones as "gone") and waits a bounded
// time for them to finish; Add fails afterwards.
func (r *Registry) Close() {
	var dropped []*stream
	r.mu.Lock()
	r.closed = true
	for id, s := range r.streams {
		if s.connected {
			s.gone = true
			s.cancel()
		} else {
			delete(r.streams, id)
			dropped = append(dropped, s)
		}
	}
	r.mu.Unlock()
	releaseAll(dropped)
	done := make(chan struct{})
	go func() { r.running.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(closeWait):
	}
}
