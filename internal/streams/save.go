package streams

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxSaveBytes bounds a saved file: the UI's buffer is at most 16 M
// characters (≤ 48 MB of UTF-8, 1.2× while frozen), plus the prefixes it adds.
const maxSaveBytes = 96 << 20

// serveSave writes the request body to <SaveDir>/<name> without replacing
// an existing file ("name (1).log", ...) and answers {"path": ...}. Used by
// the desktop app, whose webview has no download manager; the body is a
// plain string (never a Blob: WebKitGTK).
func (h *Handler) serveSave(w http.ResponseWriter, r *http.Request) {
	dir, err := h.saveDir()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := safeFileName(r.URL.Query().Get("name"))
	// the size is bounded; so is the time a body may take to arrive
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(h.saveReadTimeout))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSaveBytes))
	if err != nil {
		http.Error(w, "body too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	path, err := writeUnique(dir, name, body)
	if err != nil {
		slog.Warn("cannot save file", "dir", dir, "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"path": path})
}

// safeFileName keeps a plain base name: no directories, no control or
// path characters, not hidden; empty → "logs.log".
func safeFileName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimLeft(strings.TrimSpace(b.String()), ".")
	if len(name) > 200 {
		name = name[:200]
	}
	if name == "" {
		name = "logs.log"
	}
	return name
}

// writeUnique creates dir/name, or "stem (N).ext" if taken, owner-only (a
// log may carry secrets).
func writeUnique(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; i < 1000; i++ {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		p := filepath.Join(dir, n)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(p)
			return "", err
		}
		return p, f.Close()
	}
	return "", errors.New("no free file name")
}
