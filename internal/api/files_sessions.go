package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

type filesSessionEntry struct {
	ref    core.Ref
	handle provider.FilesHandle
	timer  *time.Timer
}
type filesSessions struct {
	mu      sync.Mutex
	seq     uint64
	closed  bool
	entries map[string]filesSessionEntry
}

func (p *filesSessions) add(ref core.Ref, h provider.FilesHandle) string {
	p.mu.Lock()
	if p.closed || len(p.entries) >= 64 {
		p.mu.Unlock()
		_ = h.Close()
		return ""
	}
	if p.entries == nil {
		p.entries = make(map[string]filesSessionEntry)
	}
	p.seq++
	id := fmt.Sprintf("f%d", p.seq)
	timer := time.AfterFunc(time.Hour, func() { _ = p.forget(id, ref) })
	p.entries[id] = filesSessionEntry{ref: ref, handle: h, timer: timer}
	p.mu.Unlock()
	return id
}
func sameFilesRef(a, b core.Ref) bool {
	return a.Provider == b.Provider && a.Target == b.Target && a.Kind == b.Kind && a.Scope == b.Scope && a.Name == b.Name && a.UID == b.UID
}
func (p *filesSessions) get(id string, ref core.Ref) (provider.FilesHandle, error) {
	p.mu.Lock()
	e, ok := p.entries[id]
	p.mu.Unlock()
	if !ok || !sameFilesRef(ref, e.ref) {
		return nil, coded(CodeGone, errors.New("this file session is no longer available; review Files again"))
	}
	return e.handle, nil
}
func (p *filesSessions) forget(id string, ref core.Ref) error {
	p.mu.Lock()
	e, ok := p.entries[id]
	if ok && !sameFilesRef(ref, e.ref) {
		p.mu.Unlock()
		return coded(CodeGone, errors.New("file session identity changed"))
	}
	delete(p.entries, id)
	if ok {
		e.timer.Stop()
	}
	p.mu.Unlock()
	if ok {
		if err := e.handle.Close(); err != nil {
			return fromProvider(err)
		}
	}
	return nil
}
func (p *filesSessions) closeAll() {
	p.mu.Lock()
	entries := p.entries
	p.entries = nil
	p.closed = true
	p.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range entries {
		e.timer.Stop()
		wg.Add(1)
		go func() { defer wg.Done(); _ = e.handle.Close() }()
	}
	wg.Wait()
}
func (s *Service) prepareFileCommand(ctx context.Context, req FilesRequest, argv []string) (provider.ExecHandle, error) {
	if req.PreparedID != "" {
		h, err := s.fileSessions.get(req.PreparedID, req.Ref)
		if err != nil {
			return nil, err
		}
		if req.ConfigRev != "" && s.live(h.Describe()).ConfigRev != req.ConfigRev {
			return nil, coded(CodeConflict, errors.New("file connection changed; close and reopen Files"))
		}
		command, err := h.PrepareCommand(ctx, argv)
		if err != nil {
			return nil, fromProvider(err)
		}
		return command, nil
	}
	if req.Ref.Provider == "kubernetes" && req.Ref.Kind == "nodes" {
		return nil, coded("forbidden", errors.New("node Files requires a reviewed file session"))
	}
	ex, err := s.execer(ctx, req.Ref)
	if err != nil {
		return nil, err
	}
	h, err := ex.PrepareExec(ctx, req.Ref, provider.ExecRequest{Instance: req.Instance, Channel: req.Channel, Command: argv})
	if err != nil {
		return nil, fromProvider(err)
	}
	return h, nil
}
