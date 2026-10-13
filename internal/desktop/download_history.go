package desktop

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/spk/spk-ocular/internal/privatefs"
)

const downloadHistoryLimit = 200

// DownloadRecord contains local UI metadata only, never remote credentials or contents.
type DownloadRecord struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Path          string `json:"path,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
	Directory     bool   `json:"directory,omitempty"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Bytes         int64  `json:"bytes"`
	StartedAt     int64  `json:"startedAt"`
	EndedAt       int64  `json:"endedAt,omitempty"`
}

type DownloadSnapshot struct {
	Records      []DownloadRecord `json:"records"`
	StorageError bool             `json:"storageError"`
}

type DownloadDeletePreview struct {
	Token     string `json:"token"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
}

type downloadDeletePlan struct {
	token, path  string
	item, parent os.FileInfo
	expires      time.Time
}

type downloadHistory struct {
	mu           sync.Mutex
	deletes      map[string]downloadDeletePlan
	records      []DownloadRecord
	lastNotify   map[string]time.Time
	path         string
	lock         *flock.Flock
	storageError bool
	writable     bool
	closed       bool
	changed      func()
	launch       func(context.Context, string, []string) error
}

func newDownloadHistory(directory string) *downloadHistory {
	h := &downloadHistory{records: []DownloadRecord{}, lastNotify: map[string]time.Time{}, deletes: map[string]downloadDeletePlan{}, launch: launchDownloadPath}
	if directory == "" {
		return h
	}
	directory = filepath.Join(directory, "downloads")
	h.path = filepath.Join(directory, "history.json")
	if err := privatefs.EnsureDir(directory); err != nil {
		h.storageError = true
		return h
	}
	h.lock = flock.New(filepath.Join(directory, "history.lock"))
	locked, err := h.lock.TryLock()
	if err != nil || !locked {
		h.storageError = true
		return h
	}
	f, err := os.Open(h.path)
	if errors.Is(err, os.ErrNotExist) {
		h.writable = true
		return h
	}
	if err != nil {
		h.storageError = true
		return h
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	if err != nil || len(data) > 16<<20 {
		h.storageError = true
		return h
	}
	var records []DownloadRecord
	if json.Unmarshal(data, &records) != nil || len(records) > 4096 {
		h.storageError = true
		return h
	}
	ids := map[string]bool{}
	for i := range records {
		r := &records[i]
		if r.ID == "" || len(r.ID) > 100 || ids[r.ID] || r.Name == "" || len(r.Name) > 32768 || r.Bytes < 0 || r.StartedAt < 0 || r.EndedAt < 0 || r.Kind != "logs" && r.Kind != "files" || !validDownloadStatus(r.Status) || r.Path != "" && (!filepath.IsAbs(r.Path) || len(r.Path) > 32768 || strings.ContainsRune(r.Path, 0)) {
			h.storageError = true
			return h
		}
		ids[r.ID] = true
		if r.Status == "failed" {
			r.FailureReason = safeDownloadFailure(r.FailureReason)
		} else {
			r.FailureReason = ""
		}
		if r.Status == "active" {
			r.Status = "interrupted"
			r.EndedAt = time.Now().UnixMilli()
			r.Path = ""
		}
	}
	h.records = records
	h.trimLocked()
	h.writable = true
	h.saveLocked()
	return h
}

func validDownloadStatus(status string) bool {
	return status == "active" || status == "complete" || status == "failed" || status == "cancelled" || status == "interrupted"
}

func (h *downloadHistory) saveLocked() {
	if h.path == "" {
		return
	}
	if !h.writable {
		h.storageError = true
		return
	}
	data, err := json.Marshal(h.records)
	if err != nil || len(data) > 16<<20 {
		h.storageError = true
		return
	}
	f, err := privatefs.CreateTemp(filepath.Dir(h.path), ".history-*")
	if err != nil {
		h.storageError = true
		return
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), h.path)
	}
	h.storageError = err != nil
}

func (h *downloadHistory) notify() {
	if h.changed != nil {
		h.changed()
	}
}

func (h *downloadHistory) begin(destination, kind string) string {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ""
	}
	id := rand.Text()
	h.records = append([]DownloadRecord{{ID: id, Name: filepath.Base(destination), Kind: kind, Status: "active", StartedAt: time.Now().UnixMilli()}}, h.records...)
	h.trimLocked()
	h.saveLocked()
	h.mu.Unlock()
	h.notify()
	return id
}

func (h *downloadHistory) trimLocked() {
	// Active transfers are never evicted. Terminal records fill the remaining slots.
	active := 0
	for _, r := range h.records {
		if r.Status == "active" {
			active++
		}
	}
	limit := max(0, downloadHistoryLimit-active)
	kept := h.records[:0]
	terminal := 0
	for _, r := range h.records {
		if r.Status == "active" || terminal < limit {
			kept = append(kept, r)
			if r.Status != "active" {
				terminal++
			}
		}
	}
	h.records = kept
	for id := range h.deletes {
		found := false
		for _, r := range kept {
			if r.ID == id {
				found = true
				break
			}
		}
		if !found {
			delete(h.deletes, id)
		}
	}
}

func (h *downloadHistory) progress(id string, bytes int64) {
	h.mu.Lock()
	notify := false
	for i := range h.records {
		r := &h.records[i]
		if r.ID == id && r.Status == "active" && bytes >= r.Bytes {
			r.Bytes = bytes
			if time.Since(h.lastNotify[id]) >= 250*time.Millisecond {
				h.lastNotify[id] = time.Now()
				notify = true
			}
			break
		}
	}
	h.mu.Unlock()
	if notify {
		h.notify()
	}
}

func (h *downloadHistory) finish(id, destination, status string, failure ...string) {
	if id == "" {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	for i := range h.records {
		r := &h.records[i]
		if r.ID == id && r.Status == "active" {
			r.Status = status
			r.FailureReason = ""
			if status == "failed" {
				reason := "unknown"
				if len(failure) > 0 {
					reason = failure[0]
				}
				r.FailureReason = safeDownloadFailure(reason)
			}
			r.EndedAt = time.Now().UnixMilli()
			if status == "complete" {
				r.Path = destination
				if info, err := os.Lstat(destination); err == nil {
					r.Directory = info.IsDir()
				}
			}
			break
		}
	}
	delete(h.lastNotify, id)
	h.trimLocked()
	h.saveLocked()
	h.mu.Unlock()
	h.notify()
}

func (h *downloadHistory) snapshot() DownloadSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	return DownloadSnapshot{Records: append([]DownloadRecord{}, h.records...), StorageError: h.storageError}
}

func (h *downloadHistory) clearCompleted() {
	h.mu.Lock()
	kept := h.records[:0]
	for _, r := range h.records {
		if r.Status == "active" {
			kept = append(kept, r)
		}
	}
	h.records = kept
	clear(h.deletes)
	h.saveLocked()
	h.mu.Unlock()
	h.notify()
}

// dismiss removes terminal metadata only; active jobs cannot disappear mid-transfer.
func (h *downloadHistory) dismiss(id string) error {
	h.mu.Lock()
	for i, r := range h.records {
		if r.ID != id {
			continue
		}
		if h.closed || r.Status == "active" {
			h.mu.Unlock()
			return errors.New("download is unavailable")
		}
		h.records = append(h.records[:i], h.records[i+1:]...)
		delete(h.deletes, id)
		h.saveLocked()
		h.mu.Unlock()
		h.notify()
		return nil
	}
	h.mu.Unlock()
	return errors.New("download is unavailable")
}

func (h *downloadHistory) prepareDelete(id string) (DownloadDeletePreview, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return DownloadDeletePreview{}, errors.New("download is unavailable")
	}
	for key, plan := range h.deletes {
		if time.Now().After(plan.expires) {
			delete(h.deletes, key)
		}
	}
	for _, r := range h.records {
		if r.ID != id || r.Status != "complete" || r.Path == "" {
			continue
		}
		path := filepath.Clean(r.Path)
		if !filepath.IsAbs(path) || filepath.Dir(path) == path {
			break
		}
		root, err := os.OpenRoot(filepath.Dir(path))
		if err != nil {
			break
		}
		item, itemErr := root.Lstat(filepath.Base(path))
		parent, parentErr := root.Stat(".")
		_ = root.Close()
		if itemErr != nil || parentErr != nil || !item.IsDir() && !item.Mode().IsRegular() {
			break
		}
		token := rand.Text()
		h.deletes[id] = downloadDeletePlan{token: token, path: path, item: item, parent: parent, expires: time.Now().Add(2 * time.Minute)}
		return DownloadDeletePreview{Token: token, Path: path, Directory: item.IsDir()}, nil
	}
	return DownloadDeletePreview{}, errors.New("downloaded item is missing or unavailable")
}

func (h *downloadHistory) cancelDelete(id, token string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deletes[id].token == token {
		delete(h.deletes, id)
	}
}

// A one-shot preview binds confirmation to the current item and its parent.
// Root confines recursive removal and does not traverse symlink destinations.
func (h *downloadHistory) deleteFile(id, token string) error {
	h.mu.Lock()
	plan, ok := h.deletes[id]
	if !ok || token == "" || plan.token != token {
		h.mu.Unlock()
		return errors.New("delete confirmation is unavailable")
	}
	delete(h.deletes, id)
	if h.closed || time.Now().After(plan.expires) {
		h.mu.Unlock()
		return errors.New("delete confirmation expired")
	}
	index := -1
	for i, r := range h.records {
		if r.ID == id && r.Status == "complete" && filepath.Clean(r.Path) == plan.path {
			index = i
			break
		}
	}
	if index < 0 {
		h.mu.Unlock()
		return errors.New("download is unavailable")
	}
	root, err := os.OpenRoot(filepath.Dir(plan.path))
	if err != nil {
		h.mu.Unlock()
		return errors.New("downloaded item is unavailable")
	}
	defer root.Close()
	parent, parentErr := root.Stat(".")
	item, itemErr := root.Lstat(filepath.Base(plan.path))
	if parentErr != nil || itemErr != nil || !os.SameFile(parent, plan.parent) || !os.SameFile(item, plan.item) || item.Mode() != plan.item.Mode() || item.Size() != plan.item.Size() || !item.ModTime().Equal(plan.item.ModTime()) {
		h.mu.Unlock()
		return errors.New("downloaded item changed; confirm again")
	}
	if item.IsDir() {
		err = root.RemoveAll(filepath.Base(plan.path))
	} else {
		err = root.Remove(filepath.Base(plan.path))
	}
	if err != nil {
		h.mu.Unlock()
		return errors.New("could not delete the downloaded item")
	}
	h.records = append(h.records[:index], h.records[index+1:]...)
	h.saveLocked()
	h.mu.Unlock()
	h.notify()
	return nil
}

func (h *downloadHistory) open(ctx context.Context, id string, reveal bool) error {
	if len(id) > 100 {
		return errors.New("invalid download ID")
	}
	h.mu.Lock()
	path := ""
	for _, r := range h.records {
		if r.ID == id && r.Status == "complete" {
			path = r.Path
			break
		}
	}
	h.mu.Unlock()
	if path == "" {
		return errors.New("download is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
		return errors.New("downloaded item is missing or unavailable")
	}
	name, args := downloadCommand(runtime.GOOS, path, reveal)
	if name == "" {
		return errors.New("file opening is unavailable on this platform")
	}
	return h.launch(ctx, name, args)
}

func downloadCommand(platform, path string, reveal bool) (string, []string) {
	switch platform {
	case "linux":
		if reveal {
			path = filepath.Dir(path)
		}
		return "xdg-open", []string{path}
	case "darwin":
		if reveal {
			return "open", []string{"-R", path}
		}
		return "open", []string{"--", path}
	case "windows":
		if reveal {
			return "explorer.exe", []string{"/select," + path}
		}
		return "rundll32.exe", []string{"url.dll,FileProtocolHandler", path}
	default:
		return "", nil
	}
}

func launchDownloadPath(ctx context.Context, name string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Associated applications may outlive Ocular. Reap the dispatch process,
	// without tying a document viewer's lifetime to our shutdown or a timeout.
	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return errors.New("could not open the downloaded item or folder")
	}
	go func() { _ = command.Wait() }()
	return nil
}

func (h *downloadHistory) close() {
	h.mu.Lock()
	h.closed = true
	for i := range h.records {
		r := &h.records[i]
		if r.Status == "active" {
			r.Status = "interrupted"
			r.Path = ""
			r.EndedAt = time.Now().UnixMilli()
		}
	}
	h.saveLocked()
	h.mu.Unlock()
	if h.lock != nil {
		_ = h.lock.Close()
	}
}
