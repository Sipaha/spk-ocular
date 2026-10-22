package krr

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"
)

var ErrCleanup = errors.New("KRR cleanup is unconfirmed; durable recovery remains pending")

type RunTarget struct {
	Provider  string `json:"provider"`
	Target    string `json:"target"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type Run struct {
	RunTarget
	ID             string `json:"id"`
	State          string `json:"state"`
	ReportID       string `json:"reportID,omitempty"`
	CleanupPending bool   `json:"cleanupPending"`
	CreatedAt      int64  `json:"createdAt"`
	ExpiresAt      int64  `json:"expiresAt"`
}
type managedRun struct {
	run    Run
	cancel context.CancelFunc
	done   chan struct{}
}

// Manager owns bounded asynchronous executions independently of panel/request
// contexts. Only the admitted connection, explicit Cancel or app close ends a run.
type Manager struct {
	mu      sync.Mutex
	runs    map[string]*managedRun
	reports *Registry
	now     func() time.Time
	closed  bool
}

func NewManager(reports *Registry, now func() time.Time) *Manager {
	return &Manager{runs: map[string]*managedRun{}, reports: reports, now: now}
}
func (m *Manager) pruneLocked() {
	for id, r := range m.runs {
		if r.cancel == nil && !m.now().Before(time.UnixMilli(r.run.ExpiresAt)) {
			delete(m.runs, id)
		}
	}
}
func (m *Manager) Start(connection context.Context, execution Execution, targets ...RunTarget) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	active := 0
	for _, r := range m.runs {
		if r.cancel != nil {
			active++
		}
	}
	oldest := ""
	if len(m.runs) >= 8 {
		for id, entry := range m.runs {
			if entry.cancel == nil && (oldest == "" || entry.run.CreatedAt < m.runs[oldest].run.CreatedAt || (entry.run.CreatedAt == m.runs[oldest].run.CreatedAt && id < oldest)) {
				oldest = id
			}
		}
	}
	if m.closed || (len(m.runs) >= 8 && oldest == "") || active >= 2 {
		return Run{}, ErrLimit
	}
	reservation, e := m.reports.Reserve()
	if e != nil {
		return Run{}, e
	}
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		m.reports.Release(reservation)
		return Run{}, ErrLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	stop := context.AfterFunc(connection, cancel)
	run := Run{ID: hex.EncodeToString(nonce[:]), State: "running", CreatedAt: m.now().UnixMilli(), ExpiresAt: m.now().Add(2 * time.Hour).UnixMilli()}
	if len(targets) > 0 {
		run.RunTarget = targets[0]
	}
	if oldest != "" {
		delete(m.runs, oldest)
	}
	entry := &managedRun{run: run, cancel: cancel, done: make(chan struct{})}
	m.runs[run.ID] = entry
	go func() {
		defer close(entry.done)
		defer m.reports.Release(reservation)
		defer cancel()
		defer stop()
		data, err := execution.Execute(ctx)
		state := "succeeded"
		reportID := ""
		if len(data) > 0 {
			// Execution can return an admitted report alongside a cleanup error.
			// Retain that useful result even when its run was cancelled during cleanup.
			stored, registerErr := m.reports.RegisterRun(context.Background(), data, reservation)
			if registerErr != nil {
				err = errors.Join(err, registerErr)
			} else {
				reportID = stored.Summary.ID
			}
		} else if err == nil {
			err = ErrInvalid
		}
		if err != nil {
			state = "failed"
			if ctx.Err() != nil {
				state = "cancelled"
			}
		}
		m.mu.Lock()
		entry.run.State = state
		entry.run.ReportID = reportID
		entry.run.CleanupPending = errors.Is(err, ErrCleanup)
		entry.cancel = nil
		m.mu.Unlock()
	}()
	return run, nil
}
func (m *Manager) List() []Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	out := []Run{}
	for _, r := range m.runs {
		out = append(out, r.run)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt || out[i].CreatedAt == out[j].CreatedAt && out[i].ID < out[j].ID
	})
	return out
}
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[id]
	if !ok {
		return ErrGone
	}
	if r.cancel != nil {
		r.run.State = "cancelling"
		r.cancel()
	}
	return nil
}
func (m *Manager) Close(ctx context.Context) {
	m.mu.Lock()
	m.closed = true
	wait := make([]chan struct{}, 0, len(m.runs))
	for _, r := range m.runs {
		if r.cancel != nil {
			r.cancel()
			wait = append(wait, r.done)
		}
	}
	m.mu.Unlock()
	for _, done := range wait {
		select {
		case <-done:
		case <-ctx.Done():
			return
		}
	}
}
