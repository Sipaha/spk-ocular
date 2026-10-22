// Package mcpclientconfig installs Ocular entries into local client settings.
package mcpclientconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/pelletier/go-toml/v2"
	"github.com/spk/spk-ocular/internal/privatefs"
)

const maxConfigBytes = 2 << 20

var (
	ErrChanged     = errors.New("client settings changed; inspect them again")
	ErrUnavailable = errors.New("automatic client setup is unavailable; use manual settings")
)

type Status struct {
	Client   string `json:"client"`
	Path     string `json:"path"`
	State    string `json:"state"`
	Revision string `json:"revision"`
}

type Manager struct {
	Home     func() (string, error)
	Getenv   func(string) string
	LookPath func(string) (string, error)
}

func New(getenv func(string) string) *Manager {
	if getenv == nil {
		getenv = os.Getenv
	}
	return &Manager{Home: os.UserHomeDir, Getenv: getenv, LookPath: exec.LookPath}
}

func (m *Manager) path(client string) (string, error) {
	home, err := m.Home()
	if err != nil {
		return "", ErrUnavailable
	}
	var path string
	switch client {
	case "codex":
		dir := m.Getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		path = filepath.Join(dir, "config.toml")
	case "claude":
		if dir := m.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
			path = filepath.Join(dir, ".claude.json")
		} else {
			path = filepath.Join(home, ".claude.json")
		}
	default:
		return "", ErrUnavailable
	}
	if !filepath.IsAbs(path) {
		return "", ErrUnavailable
	}
	return filepath.Clean(path), nil
}

// Reject managed links rather than changing their target or replacing the link.
func regularPath(path string) error {
	for p := path; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return ErrUnavailable
			}
			if p == path && !info.Mode().IsRegular() {
				return ErrUnavailable
			}
			if p != path && !info.IsDir() {
				return ErrUnavailable
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return ErrUnavailable
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func read(path string) ([]byte, bool, error) {
	if err := regularPath(path); err != nil {
		return nil, false, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrUnavailable
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil || len(b) > maxConfigBytes {
		return nil, true, ErrUnavailable
	}
	return b, true, nil
}

func revision(path string, data []byte, exists bool, wanted string) string {
	sum := sha256.Sum256([]byte(path + "\x00" + string(data) + "\x00" + string([]byte{boolByte(exists)}) + "\x00" + wanted))
	return hex.EncodeToString(sum[:])
}
func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

func (m *Manager) inspect(client, wanted string) (Status, []byte, []byte, error) {
	path, err := m.path(client)
	status := Status{Client: client, Path: path, State: "unavailable"}
	if err != nil || wanted == "" {
		return status, nil, nil, ErrUnavailable
	}
	old, exists, err := read(path)
	if err != nil {
		return status, nil, nil, err
	}
	status.Revision = revision(path, old, exists, wanted)
	if _, found := m.LookPath(client); found != nil && !exists {
		status.State = "not_detected"
		return status, old, nil, nil
	}
	next, state, err := merge(client, old, wanted)
	if err != nil || len(next) > maxConfigBytes {
		status.State = "invalid"
		return status, old, nil, nil
	}
	status.State = state
	return status, old, next, nil
}

func (m *Manager) Inspect(client, wanted string) Status {
	status, _, _, _ := m.inspect(client, wanted)
	return status
}

// Install writes only a prepared, still-current entry. Other settings retain
// their original bytes. A foreign ocular entry is never replaced automatically.
func (m *Manager) Install(ctx context.Context, client, wanted, expected string) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{Client: client, State: "unavailable"}, err
	}
	status, _, _, err := m.inspect(client, wanted)
	if err != nil || (status.State != "ready" && status.State != "configured") {
		return status, ErrUnavailable
	}
	if expected == "" || expected != status.Revision {
		return status, ErrChanged
	}
	if err := os.MkdirAll(filepath.Dir(status.Path), 0o700); err != nil {
		return status, ErrUnavailable
	}
	lockPath := status.Path + ".ocular.lock"
	if err := regularPath(lockPath); err != nil {
		return status, err
	}
	if f, err := privatefs.CreateNew(lockPath); err == nil {
		_ = f.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return status, ErrUnavailable
	}
	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	defer func() { _ = lock.Close() }()
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ok, err := lock.TryLockContext(lockCtx, 20*time.Millisecond)
	if err != nil || !ok {
		return status, ErrUnavailable
	}
	status, old, next, err := m.inspect(client, wanted)
	if err != nil || status.Revision != expected {
		return status, ErrChanged
	}
	if status.State == "configured" {
		return status, nil
	}
	if status.State != "ready" || next == nil || len(next) > maxConfigBytes {
		return status, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	f, err := privatefs.CreateTemp(filepath.Dir(status.Path), ".ocular-mcp-*")
	if err != nil {
		return status, ErrUnavailable
	}
	temp := f.Name()
	defer func() { _ = os.Remove(temp) }()
	_, writeErr := f.Write(next)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return status, ErrUnavailable
	}
	current, exists, err := read(status.Path)
	if err != nil || !bytes.Equal(current, old) || revision(status.Path, current, exists, wanted) != expected {
		return status, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return status, err
	}
	if err := os.Rename(temp, status.Path); err != nil {
		return status, ErrUnavailable
	}
	return m.Inspect(client, wanted), nil
}

func merge(client string, old []byte, wanted string) ([]byte, string, error) {
	switch client {
	case "codex":
		return mergeTOML(old, wanted)
	case "claude":
		return mergeJSON(old, wanted)
	default:
		return nil, "invalid", ErrUnavailable
	}
}

func mergeTOML(old []byte, wanted string) ([]byte, string, error) {
	var existing, desired map[string]any
	if err := toml.Unmarshal(old, &existing); err != nil {
		return nil, "invalid", err
	}
	if err := toml.Unmarshal([]byte(wanted), &desired); err != nil {
		return nil, "invalid", err
	}
	servers, _ := existing["mcp_servers"].(map[string]any)
	wantServers, _ := desired["mcp_servers"].(map[string]any)
	want, _ := wantServers["ocular"].(map[string]any)
	if want == nil || want["url"] == nil || want["http_headers"] == nil {
		return nil, "invalid", ErrUnavailable
	}
	if entry, ok := servers["ocular"]; ok {
		value, ok := entry.(map[string]any)
		headers, _ := value["http_headers"].(map[string]any)
		wantHeaders, _ := want["http_headers"].(map[string]any)
		if ok && value["command"] == nil && value["url"] == want["url"] && headers["Authorization"] == wantHeaders["Authorization"] {
			return nil, "configured", nil
		}
		return nil, "conflict", nil
	}
	next := append(append([]byte{}, old...), []byte("\n"+wanted)...)
	var verified map[string]any
	if err := toml.Unmarshal(next, &verified); err != nil {
		return nil, "invalid", err
	}
	return next, "ready", nil
}

type jsonField struct {
	raw        json.RawMessage
	start, end int
}

// Values stay raw, preserving unrelated settings, numbers and whitespace.
func object(data []byte) (map[string]jsonField, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, ErrUnavailable
	}
	fields := map[string]jsonField{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrUnavailable
		}
		if _, exists := fields[key]; exists {
			return nil, ErrUnavailable
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		end := int(decoder.InputOffset())
		fields[key] = jsonField{raw: raw, start: end - len(raw), end: end}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, err
	}
	if !json.Valid(data) {
		return nil, ErrUnavailable
	}
	return fields, nil
}

func insert(data []byte, property string, hasFields bool) []byte {
	end := len(bytes.TrimRight(data, " \r\n\t")) - 1
	comma := ""
	if hasFields {
		comma = ","
	}
	return append(append(append([]byte{}, data[:end]...), []byte(comma+"\n"+property+"\n")...), data[end:]...)
}

func mergeJSON(old []byte, wanted string) ([]byte, string, error) {
	var desired struct {
		Servers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(wanted), &desired); err != nil || desired.Servers["ocular"] == nil {
		return nil, "invalid", ErrUnavailable
	}
	if len(bytes.TrimSpace(old)) == 0 {
		old = []byte("{}")
	}
	fields, err := object(old)
	if err != nil {
		return nil, "invalid", err
	}
	servers, ok := fields["mcpServers"]
	property := `"ocular":` + string(desired.Servers["ocular"])
	if !ok {
		next := insert(old, `"mcpServers":{`+property+`}`, len(fields) > 0)
		if !json.Valid(next) {
			return nil, "invalid", ErrUnavailable
		}
		return next, "ready", nil
	}
	entries, err := object(servers.raw)
	if err != nil {
		return nil, "invalid", err
	}
	entry, exists := entries["ocular"]
	if exists {
		var value, want struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Command json.RawMessage   `json:"command"`
			Headers map[string]string `json:"headers"`
		}
		if json.Unmarshal(entry.raw, &value) != nil || json.Unmarshal(desired.Servers["ocular"], &want) != nil {
			return nil, "conflict", nil
		}
		if value.URL != want.URL || value.Headers["Authorization"] != want.Headers["Authorization"] || len(value.Command) > 0 || (value.Type != "http" && value.Type != "streamable-http" && value.Type != "") {
			return nil, "conflict", nil
		}
		if value.Type != "" {
			return nil, "configured", nil
		}
		entryFields, err := object(entry.raw)
		if err != nil {
			return nil, "invalid", err
		}
		repaired := insert(entry.raw, `"type":"http"`, len(entryFields) > 0)
		next := append(append(append([]byte{}, old[:servers.start+entry.start]...), repaired...), old[servers.start+entry.end:]...)
		if !json.Valid(next) {
			return nil, "invalid", ErrUnavailable
		}
		return next, "ready", nil
	}
	added := insert(servers.raw, property, len(entries) > 0)
	next := append(append(append([]byte{}, old[:servers.start]...), added...), old[servers.end:]...)
	if !json.Valid(next) {
		return nil, "invalid", ErrUnavailable
	}
	return next, "ready", nil
}
