// Package streams serves long-lived streams (container logs; later exec)
// to the UI over plain HTTP: in desktop mode from a token-protected loopback
// server (never wails://: WebKitGTK buffers and truncates streams there), in
// browser mode from the main server under /streams/. A stream is opened in
// two steps: the API validates the request and registers a Func under a
// single-use id (Registry.Add), then the page GETs <base>/logs/<id> and the
// Func writes NDJSON frames to the response until it ends, the page goes
// away, or its owner (a provider session incarnation) is closed.
package streams

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Func runs one stream: it writes frames to out until ctx ends (the page
// went away, the owner closed) or the stream is complete. Its error becomes
// the final "end" frame.
type Func func(ctx context.Context, out *Writer) error

const (
	// connectTTL: a registered stream the page did not connect to within
	// this long is dropped (the page went away between the two steps).
	connectTTL = 30 * time.Second
	// MaxStreams bounds registered + connected streams of the whole app.
	MaxStreams = 8
)

var (
	// ErrGone: unknown, already used, expired or revoked stream id / owner.
	ErrGone = errors.New("stream is gone")
	// ErrLimit: too many open streams.
	ErrLimit = fmt.Errorf("too many open streams (max %d)", MaxStreams)
)

type stream struct {
	owner     string
	run       Func
	created   time.Time
	connected bool
	cancel    context.CancelFunc // set on connect
	gone      bool               // owner closed: the end frame says so
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
}

func NewRegistry() *Registry {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return &Registry{epoch: hex.EncodeToString(b), now: time.Now, streams: map[string]*stream{}}
}

// Add registers run for owner and returns the id the page connects with.
// The caller must make sure owner is alive at this moment (the API calls it
// under the lock that also guards closing the owner), so a stream can never
// outlive a closed owner.
func (r *Registry) Add(owner string, run Func) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", ErrGone
	}
	r.dropExpiredLocked()
	if len(r.streams) >= MaxStreams {
		return "", ErrLimit
	}
	r.seq++
	id := fmt.Sprintf("s%s-%d", r.epoch, r.seq)
	r.streams[id] = &stream{owner: owner, run: run, created: r.now()}
	return id, nil
}

// dropExpiredLocked removes registered streams nobody connected to in time.
func (r *Registry) dropExpiredLocked() {
	for id, s := range r.streams {
		if !s.connected && r.now().Sub(s.created) > connectTTL {
			delete(r.streams, id)
		}
	}
}

// connect takes a registered stream (once) and gives it a context that the
// owner's closing cancels. done must be called when the stream has ended.
func (r *Registry) connect(parent context.Context, id string) (s *stream, ctx context.Context, done func(), err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropExpiredLocked()
	s = r.streams[id]
	if r.closed || s == nil || s.connected {
		return nil, nil, nil, ErrGone
	}
	s.connected = true
	ctx, s.cancel = context.WithCancel(parent)
	cancel := s.cancel
	return s, ctx, func() {
		cancel()
		r.mu.Lock()
		delete(r.streams, id)
		r.mu.Unlock()
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
		}
	}
}

// Owners counts live (registered or connected) streams per owner: a session
// with an open log tab is in use even without table views.
func (r *Registry) Owners() map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropExpiredLocked()
	out := map[string]int{}
	for _, s := range r.streams {
		out[s.owner]++
	}
	return out
}

// Len is the number of live streams (for /api/_test/stats).
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropExpiredLocked()
	return len(r.streams)
}

// Close ends every stream; Add fails afterwards.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for id, s := range r.streams {
		if s.connected {
			s.cancel()
		} else {
			delete(r.streams, id)
		}
	}
}
