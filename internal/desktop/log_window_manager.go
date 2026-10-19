//go:build wails

package desktop

import (
	"encoding/json"
	"errors"
	"github.com/spk/spk-ocular/internal/paths"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const logWindowEvent = "ocular_log_window"

var logWindowID = regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`)

// LogWindows is an application UI service, deliberately outside the agent API.
// The main window owns the log stream; secondary windows receive its frames.
type LogWindows struct {
	app           *application.App
	mainWindow    *application.WebviewWindow
	mu            sync.Mutex
	openMu        sync.Mutex
	windows       map[string]*application.WebviewWindow
	exports       map[string]*nativeLogExport
	exportsClosed bool
	history       *downloadHistory
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
	var screen *application.Screen
	if m.mainWindow != nil {
		screen, _ = m.mainWindow.GetScreen()
	}
	screen = detachedLogScreen(runtime.GOOS, screen, m.app.Screen.GetAll(), m.app.Screen.GetPrimary())
	width, height := detachedLogSize(screen)
	w := m.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "logs-" + id, Title: title + " · SPK Ocular", Hidden: true, Width: width, Height: height, MinWidth: min(480, width), MinHeight: min(280, height), Screen: screen, InitialPosition: application.WindowCentered, URL: "/?logsWindow=" + id, BackgroundColour: application.NewRGBA(21, 24, 30, 255), DevToolsEnabled: devToolsEnabled, Linux: application.LinuxWindow{WebviewGpuPolicy: webviewGPUPolicy(os.Getenv("SPK_OCULAR_GPU"))}})
	configureWindowIcons(w)
	w.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		m.abortWindowExports(id)
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

// ExportLogs saves buffered logs to the default download directory without replacing files.
func (m *LogWindows) ExportLogs(_ string, name, title, button, text string) (string, error) {
	if len(name) > 200 || len(title) > 256 || len(button) > 80 || len(text) > 96<<20 {
		return "", errors.New("log download exceeds its limits")
	}
	directory, err := paths.Downloads(os.Getenv)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	destination := filepath.Join(directory, filepath.Base(name))
	downloadID := m.history.begin(destination, "logs")
	staged, err := newStagedLogExport(destination, 96<<20)
	if err == nil {
		staged.unique = true
		defer staged.Abort()
		if err = staged.Write(text); err == nil {
			err = staged.Complete()
		}
		destination = staged.destination
	}
	if err != nil {
		m.history.finish(downloadID, "", "failed", downloadFailureReason(err, "write"))
		return "", err
	}
	m.history.progress(downloadID, int64(len(text)))
	m.history.finish(downloadID, destination, "complete")
	return destination, nil
}
