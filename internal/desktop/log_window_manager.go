//go:build wails

package desktop

import (
	"encoding/json"
	"errors"
	"github.com/spk/spk-ocular/internal/paths"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const logWindowEvent = "ocular_log_window"

var logWindowID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`)

// LogWindows is an application UI service, deliberately outside the agent API.
// The main window owns the log stream; secondary windows receive its frames.
type LogWindows struct {
	app        *application.App
	mainWindow *application.WebviewWindow
	mu         sync.Mutex
	openMu     sync.Mutex
	windows    map[string]*application.WebviewWindow
}

func (m *LogWindows) Open(id, title string) error {
	if !logWindowID.MatchString(id) || len(title) > 512 {
		return errors.New("invalid log window")
	}
	m.openMu.Lock()
	defer m.openMu.Unlock()
	m.mu.Lock()
	if w := m.windows[id]; w != nil {
		m.mu.Unlock()
		w.Show()
		w.Focus()
		return nil
	}
	if len(m.windows) >= 8 {
		m.mu.Unlock()
		return errors.New("at most eight detached log windows")
	}
	m.mu.Unlock()
	w := m.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "logs-" + id, Title: title + " · SPK Ocular", Hidden: true, Width: 1000, Height: 650, MinWidth: 480, MinHeight: 280, URL: "/?logsWindow=" + id, BackgroundColour: application.NewRGBA(21, 24, 30, 255), DevToolsEnabled: devToolsEnabled, Linux: application.LinuxWindow{WebviewGpuPolicy: webviewGPUPolicy(os.Getenv("SPK_OCULAR_GPU"))}})
	configureWindowIcons(w)
	w.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		m.mu.Lock()
		delete(m.windows, id)
		m.mu.Unlock()
		m.app.Event.Emit(logWindowEvent, map[string]any{"id": id, "sender": "host", "type": "closed"})
	})
	m.mu.Lock()
	m.windows[id] = w
	m.mu.Unlock()
	w.Show()
	w.Focus()
	return nil
}
func (m *LogWindows) Close(id string) error {
	m.mu.Lock()
	w := m.windows[id]
	m.mu.Unlock()
	if w != nil {
		w.Close()
	}
	return nil
}
func (m *LogWindows) Send(id, sender, kind string, payload string) error {
	if !logWindowID.MatchString(id) || len(sender) > 80 || len(kind) > 40 || len(payload) > 96<<20 {
		return errors.New("invalid log window message")
	}
	// Events travel directly through Wails: the coalescing resource-event queue
	// must never drop or reorder log frames. No contents are persisted.
	var data any
	if len(payload) > 0 {
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			return err
		}
	}
	m.app.Event.Emit(logWindowEvent, map[string]any{"id": id, "sender": sender, "type": kind, "payload": data})
	return nil
}

// ExportLogs prompts in the requesting native window, then writes only to the
// path the user chose. Cancel returns an empty path and writes nothing.
func (m *LogWindows) ExportLogs(id, name, title, button, text string) (string, error) {
	if len(name) > 200 || len(title) > 256 || len(button) > 80 || len(text) > 96<<20 {
		return "", errors.New("log download exceeds its limits")
	}
	m.mu.Lock()
	parent := m.windows[id]
	if parent == nil {
		parent = m.mainWindow
	}
	m.mu.Unlock()
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
	if err = writeLogExport(destination, text); err != nil {
		return "", err
	}
	return destination, nil
}
