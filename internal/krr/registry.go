package krr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrLimit = errors.New("KRR report storage limit reached; discard an existing report")
var ErrGone = errors.New("KRR report is unavailable")

type Summary struct {
	ID        string `json:"id"`
	Origin    string `json:"origin"`
	Version   string `json:"version"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Rows      int    `json:"rows"`
}
type Stored struct {
	Summary Summary `json:"summary"`
	Report  Report  `json:"report"`
}
type entry struct {
	summary Summary
	data    []byte
}

// Registry belongs to the application service, independently of mounted panels.
// It stores only projected JSON, never source input, filename or credentials.
type Registry struct {
	slots    chan struct{}
	mu       sync.Mutex
	entries  map[string]entry
	now      func() time.Time
	closed   bool
	reserved map[string]bool
}

func NewRegistry(now func() time.Time) *Registry {
	return &Registry{slots: make(chan struct{}, 2), entries: map[string]entry{}, now: now, reserved: map[string]bool{}}
}
func (r *Registry) pruneLocked() {
	for id, e := range r.entries {
		if !r.now().Before(time.UnixMilli(e.summary.ExpiresAt)) {
			delete(r.entries, id)
		}
	}
}
func (r *Registry) Import(ctx context.Context, data []byte, version string) (Stored, error) {
	return r.register(ctx, data, version, "file", "")
}

// RegisterRun independently validates output from the pinned external container.
func (r *Registry) RegisterRun(ctx context.Context, data []byte, reservation string) (Stored, error) {
	return r.register(ctx, data, "1.31.0", "job", reservation)
}
func (r *Registry) register(ctx context.Context, data []byte, version, origin, reservation string) (Stored, error) {
	if reservation != "" {
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
		case <-ctx.Done():
			return Stored{}, ctx.Err()
		}
	} else {
		select {
		case r.slots <- struct{}{}:
			defer func() { <-r.slots }()
		default:
			return Stored{}, ErrLimit
		}
	}
	report, err := Parse(data, version)
	if err != nil {
		return Stored{}, err
	}
	if err = ctx.Err(); err != nil {
		return Stored{}, err
	}
	safe, err := json.Marshal(report)
	if err != nil {
		return Stored{}, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	if r.closed {
		return Stored{}, ErrGone
	}
	if err = ctx.Err(); err != nil {
		return Stored{}, err
	}
	if reservation != "" {
		if !r.reserved[reservation] {
			return Stored{}, ErrGone
		}
		delete(r.reserved, reservation)
	}
	if len(safe) > MaxBytes {
		return Stored{}, ErrLimit
	}
	size := len(safe) + len(r.reserved)*MaxBytes
	for _, e := range r.entries {
		size += len(e.data)
	}
	if len(r.entries)+len(r.reserved) >= 8 || size > 4<<20 {
		return Stored{}, ErrLimit
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return Stored{}, errors.New("KRR report registration failed")
	}
	summary := Summary{ID: hex.EncodeToString(nonce[:]), Origin: origin, Version: version, CreatedAt: r.now().UnixMilli(), ExpiresAt: r.now().Add(2 * time.Hour).UnixMilli(), Rows: len(report.Rows)}
	r.entries[summary.ID] = entry{summary: summary, data: safe}
	return Stored{Summary: summary, Report: report}, nil
}
func (r *Registry) List() []Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	out := []Summary{}
	if r.closed {
		return out
	}
	for _, e := range r.entries {
		out = append(out, e.summary)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt || out[i].CreatedAt == out[j].CreatedAt && out[i].ID < out[j].ID
	})
	return out
}
func (r *Registry) Get(id string) (Stored, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	e, ok := r.entries[id]
	if !ok || r.closed {
		return Stored{}, ErrGone
	}
	var report Report
	if json.Unmarshal(e.data, &report) != nil {
		return Stored{}, ErrGone
	}
	return Stored{Summary: e.summary, Report: report}, nil
}
func (r *Registry) Forget(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[id]; !ok {
		return ErrGone
	}
	delete(r.entries, id)
	return nil
}
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	clear(r.entries)
	clear(r.reserved)
}

// Reserve protects one bounded result slot before any external Job is created.
func (r *Registry) Reserve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	if r.closed {
		return "", ErrGone
	}
	size := (len(r.reserved) + 1) * MaxBytes
	for _, e := range r.entries {
		size += len(e.data)
	}
	if len(r.entries)+len(r.reserved) >= 8 || size > 4<<20 {
		return "", ErrLimit
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", ErrLimit
	}
	id := hex.EncodeToString(nonce[:])
	r.reserved[id] = true
	return id, nil
}
func (r *Registry) Release(id string) { r.mu.Lock(); defer r.mu.Unlock(); delete(r.reserved, id) }
