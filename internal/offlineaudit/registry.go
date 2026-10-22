package offlineaudit

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

var ErrLimit = errors.New("offline audit storage limit reached; forget an existing report")
var ErrGone = errors.New("offline audit report is unavailable")

type Summary struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Target    string `json:"target"`
	ConfigRev string `json:"configRev"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
	Objects   int    `json:"objects"`
	Checks    int    `json:"checks"`
}
type Stored struct {
	Summary Summary `json:"summary"`
	Report  Report  `json:"report"`
}
type entry struct {
	summary Summary
	data    []byte
}

// Registry owns only projected memory, independently of renderer panels.
// Files, source text, diagnostics and target configuration are never retained.
type Registry struct {
	mu      sync.Mutex
	entries map[string]entry
	slots   chan struct{}
	now     func() time.Time
	closed  bool
}

func NewRegistry(now func() time.Time) *Registry {
	return &Registry{entries: map[string]entry{}, slots: make(chan struct{}, 2), now: now}
}
func (r *Registry) pruneLocked() {
	for id, e := range r.entries {
		if !r.now().Before(time.UnixMilli(e.summary.ExpiresAt)) {
			delete(r.entries, id)
		}
	}
}
func (r *Registry) Import(ctx context.Context, data []byte, version, target, revision string) (Stored, error) {
	if !ValidContext(target, revision) {
		return Stored{}, ErrInvalid
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	default:
		return Stored{}, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return Stored{}, err
	}
	report, err := Parse(data, version)
	if err != nil {
		return Stored{}, err
	}
	projected, err := json.Marshal(report)
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
	size := len(projected)
	for _, e := range r.entries {
		size += len(e.data)
	}
	if len(projected) > MaxBytes || len(r.entries) >= 8 || size > 4<<20 {
		return Stored{}, ErrLimit
	}
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return Stored{}, ErrLimit
	}
	summary := Summary{ID: hex.EncodeToString(nonce[:]), Version: version, Target: target, ConfigRev: revision, CreatedAt: r.now().UnixMilli(), ExpiresAt: r.now().Add(2 * time.Hour).UnixMilli(), Objects: len(report.Subjects), Checks: report.Checks}
	r.entries[summary.ID] = entry{summary, projected}
	return Stored{summary, report}, nil
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
		return out[i].CreatedAt > out[j].CreatedAt || (out[i].CreatedAt == out[j].CreatedAt && out[i].ID < out[j].ID)
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
	return Stored{e.summary, report}, nil
}
func (r *Registry) Forget(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	if _, ok := r.entries[id]; !ok || r.closed {
		return ErrGone
	}
	delete(r.entries, id)
	return nil
}
func (r *Registry) Close() { r.mu.Lock(); defer r.mu.Unlock(); r.closed = true; clear(r.entries) }
