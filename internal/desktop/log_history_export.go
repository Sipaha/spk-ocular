//go:build wails

package desktop

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/spk/spk-ocular/internal/paths"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type nativeLogExport struct {
	staged *stagedLogExport
	owner  string
	timer  *time.Timer
}

// BeginLogExport prompts before starting history reads; an empty id is Cancel.
// Tokens authorize only app-owned staged files, never a client-supplied path.
func (m *LogWindows) BeginLogExport(windowID, name, title, button string) (string, error) {
	if len(name) > 200 || len(title) > 256 || len(button) > 80 {
		return "", errors.New("invalid log export dialog")
	}
	m.mu.Lock()
	parent := m.windows[windowID]
	owner := windowID
	if parent == nil {
		parent = m.mainWindow
		owner = ""
	}
	closed := m.exportsClosed
	m.mu.Unlock()
	if closed {
		return "", errors.New("application is closing")
	}
	directory, err := paths.Downloads(os.Getenv)
	if err != nil {
		return "", err
	}
	dialog := m.app.Dialog.SaveFile()
	dialog.SetOptions(&application.SaveFileDialogOptions{Title: title, ButtonText: button, Directory: directory, Filename: filepath.Base(name), CanCreateDirectories: true, AllowOtherFileTypes: true, Window: parent})
	destination, err := dialog.PromptForSingleSelection()
	if err != nil || destination == "" {
		return "", err
	}
	staged, err := newStagedLogExport(destination, 1<<30)
	if err != nil {
		return "", err
	}
	var raw [24]byte
	if _, err = rand.Read(raw[:]); err != nil {
		staged.Abort()
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exportsClosed || owner != "" && m.windows[owner] != parent {
		staged.Abort()
		return "", errors.New("requesting window closed")
	}
	if len(m.exports) >= 8 {
		staged.Abort()
		return "", errors.New("at most eight log exports")
	}
	if m.exports == nil {
		m.exports = map[string]*nativeLogExport{}
	}
	job := &nativeLogExport{staged: staged, owner: owner}
	m.exports[id] = job
	job.timer = time.AfterFunc(10*time.Minute, func() { _ = m.CancelLogExport(id) })
	return id, nil
}
func (m *LogWindows) AppendLogExport(id, text string) error {
	if len(text) > 1<<20 {
		return errors.New("log export chunk exceeds 1 MiB")
	}
	m.mu.Lock()
	job := m.exports[id]
	m.mu.Unlock()
	if job == nil {
		return errors.New("log export is unavailable")
	}
	return job.staged.Write(text)
}
func (m *LogWindows) CompleteLogExport(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.exports[id]
	if job == nil {
		return "", errors.New("log export is unavailable")
	}
	// Serialize final rename with window/app closure: a job remains owned until
	// either its complete file is committed or its staged file has been removed.
	defer delete(m.exports, id)
	job.timer.Stop()
	if err := job.staged.Complete(); err != nil {
		return "", err
	}
	return job.staged.destination, nil
}

func (m *LogWindows) CancelLogExport(id string) error {
	m.mu.Lock()
	job := m.exports[id]
	delete(m.exports, id)
	m.mu.Unlock()
	if job != nil {
		job.timer.Stop()
		job.staged.Abort()
	}
	return nil
}
func (m *LogWindows) abortWindowExports(owner string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, job := range m.exports {
		if job.owner == owner {
			delete(m.exports, id)
			job.timer.Stop()
			job.staged.Abort()
		}
	}
}
func (m *LogWindows) closeExports() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exportsClosed = true
	for id, job := range m.exports {
		delete(m.exports, id)
		job.timer.Stop()
		job.staged.Abort()
	}
}
