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
	staged     *stagedLogExport
	owner      string
	timer      *time.Timer
	downloadID string
}

// BeginLogExport saves to Downloads by default; explicit location selection may cancel.
// Tokens authorize only app-owned staged files, never a client-supplied path.
func (m *LogWindows) BeginLogExport(windowID, name, title, button string, chooseLocation bool) (string, error) {
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
	destination := filepath.Join(directory, filepath.Base(name))
	if chooseLocation {
		dialog := m.app.Dialog.SaveFile()
		dialog.SetOptions(&application.SaveFileDialogOptions{Title: title, ButtonText: button, Directory: directory, Filename: filepath.Base(name), CanCreateDirectories: true, AllowOtherFileTypes: true, Window: parent})
		destination, err = dialog.PromptForSingleSelection()
		if err != nil || destination == "" {
			return "", err
		}
	} else if err = os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	downloadID := m.history.begin(destination, "logs")
	staged, err := newStagedLogExport(destination, 1<<30)
	if err != nil {
		m.history.finish(downloadID, "", "failed", downloadFailureReason(err, "write"))
		return "", err
	}
	staged.unique = !chooseLocation
	var raw [24]byte
	if _, err = rand.Read(raw[:]); err != nil {
		staged.Abort()
		m.history.finish(downloadID, "", "failed", "unknown")
		return "", err
	}
	id := hex.EncodeToString(raw[:])
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.exportsClosed || owner != "" && m.windows[owner] != parent {
		staged.Abort()
		m.history.finish(downloadID, "", "interrupted")
		return "", errors.New("requesting window closed")
	}
	if len(m.exports) >= 8 {
		staged.Abort()
		m.history.finish(downloadID, "", "failed", "limit")
		return "", errors.New("at most eight log exports")
	}
	if m.exports == nil {
		m.exports = map[string]*nativeLogExport{}
	}
	job := &nativeLogExport{staged: staged, owner: owner}
	job.downloadID = downloadID
	m.exports[id] = job
	m.history.registerCancel(downloadID, func() {
		_ = m.CancelLogExport(id)
		if m.app != nil {
			m.app.Event.Emit("ocular_log_export_cancelled", id)
		}
	})
	job.timer = time.AfterFunc(10*time.Minute, func() { _ = m.endLogExport(id, "failed", "timeout") })
	return id, nil
}
func (m *LogWindows) AppendLogExport(id, text string) error {
	m.mu.Lock()
	job := m.exports[id]
	m.mu.Unlock()
	if job == nil {
		return errors.New("log export is unavailable")
	}
	if len(text) > 1<<20 {
		job.staged.Abort()
		m.history.finish(job.downloadID, "", "failed", "limit")
		return errors.New("log export chunk exceeds 1 MiB")
	}
	err := job.staged.Write(text)
	job.staged.mu.Lock()
	bytes := job.staged.bytes
	job.staged.mu.Unlock()
	m.history.progress(job.downloadID, bytes)
	if err != nil {
		m.history.finish(job.downloadID, "", "failed", downloadFailureReason(err, "write"))
	}
	return err
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
		m.history.finish(job.downloadID, "", "failed", downloadFailureReason(err, "write"))
		return "", err
	}
	m.history.finish(job.downloadID, job.staged.destination, "complete")
	return job.staged.destination, nil
}

func (m *LogWindows) CancelLogExport(id string) error {
	return m.endLogExport(id, "cancelled", "unknown")
}
func (m *LogWindows) FailLogExport(id string) error {
	return m.endLogExport(id, "failed", "unknown")
}

// FailLogExportReason accepts only a safe code, never renderer exception text.
func (m *LogWindows) FailLogExportReason(id, reason string) error {
	return m.endLogExport(id, "failed", safeDownloadFailure(reason))
}
func (m *LogWindows) endLogExport(id, status, reason string) error {
	m.mu.Lock()
	job := m.exports[id]
	delete(m.exports, id)
	m.mu.Unlock()
	if job != nil {
		job.timer.Stop()
		job.staged.Abort()
		m.history.finish(job.downloadID, "", status, reason)
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
			m.history.finish(job.downloadID, "", "cancelled")
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
		m.history.finish(job.downloadID, "", "interrupted")
	}
}
