//go:build wails

package desktop

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"
	wailevents "github.com/wailsapp/wails/v3/pkg/events"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/api/transport"
	"github.com/spk/spk-ocular/internal/events"
)

type Options struct {
	FrontendFS fs.FS
	Service    api.API
	Emitter    *events.Emitter
	IconPNG    []byte
	DataDir    string
}

// Run starts the Wails loop with one window. Closing the window quits the
// app (Wails quits on the last window closed); so does ctx cancellation.
func Run(ctx context.Context, o Options) error {
	var shutDown atomic.Bool
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	bus := sessionBusState()
	if cutOffBusFor(runtime.GOOS, bus) {
		cutOffSessionBus() // before application.New() initializes GTK
	}

	downloadSource, ok := o.Service.(interface {
		DownloadFiles(context.Context, api.FilesRequest, io.Writer) error
	})
	if !ok {
		return errors.New("container download service is unavailable")
	}
	fileDownloads := &FileDownloads{ctx: ctx, source: downloadSource}
	logWindows := &LogWindows{windows: map[string]*application.WebviewWindow{}}
	app := application.New(application.Options{
		// Warn: Info logs every asset request. Never Debug in a build that
		// ships: Wails logs binding results there.
		LogLevel:    slog.LevelWarn,
		Name:        "spk-ocular",
		Description: "Lightweight local infrastructure viewer",
		Icon:        o.IconPNG,
		Windows:     application.WindowsOptions{WebviewUserDataPath: filepath.Join(o.DataDir, "webview")},
		Services:    []application.Service{application.NewService(transport.NewAPI(o.Service)), application.NewService(logWindows), application.NewService(fileDownloads)},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},
		OnShutdown: func() { // on the GTK main thread
			shutDown.Store(true)
			cancel()
		},
	})

	logWindows.app = app
	fileDownloads.app = app

	// Relay core events to the page (Wails events; payload is the one arg);
	// agents' waiting plans also wake the window's own watch.
	pendingWake := make(chan struct{}, 1)
	go func() {
		sub, unsub := o.Emitter.Subscribe()
		defer unsub()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sub.Wake():
				for _, ev := range sub.Drain() {
					app.Event.Emit(ev.Type, ev.Payload)
					if ev.Type == api.EventAgentPendingChanged {
						select {
						case pendingWake <- struct{}{}:
						default:
						}
					}
				}
			}
		}
	}()

	win := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  windowTitle,
		Width:  1280,
		Height: 820,
		// Matches --color-app in web/src/index.css: no light flash before
		// the webview paints.
		BackgroundColour: application.NewRGBA(21, 24, 30, 255),
		URL:              "/",
		DevToolsEnabled:  devToolsEnabled,
		Linux:            application.LinuxWindow{WebviewGpuPolicy: webviewGPUPolicy(os.Getenv("SPK_OCULAR_GPU"))},
	})

	configureWindowIcons(win)
	logWindows.mainWindow = win
	fileDownloads.window = win
	win.OnWindowEvent(wailevents.Common.WindowClosing, func(_ *application.WindowEvent) { app.Quit() })

	go watchPending(ctx, o.Service, win, bus == busOK, pendingWake)

	go func() {
		<-ctx.Done()
		if !shutDown.Load() { // the caller's ctx ended (SIGINT); not our own shutdown
			app.Quit()
		}
	}()
	return app.Run()
}

// watchPending shows the agents' plans waiting for the user's confirmation
// even when the window is hidden: its title counts them, and more of them
// than before is a desktop notification (a live bus only). The window is
// never raised.
func watchPending(ctx context.Context, svc api.API, win *application.WebviewWindow, canNotify bool, wake <-chan struct{}) {
	var p pendingNotice
	var shown uint32 // the notification to replace
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		}
		st, err := svc.AgentAccessStatus(ctx)
		if err != nil {
			continue
		}
		info, _ := svc.AppInfo(ctx)
		title, notify := p.update(st.Pending, info.Language)
		win.SetTitle(title)
		if notify && canNotify {
			summary, body := notification(st.Pending, info.Language)
			id, err := sendNotification(summary, body, shown)
			if err != nil {
				slog.Warn("desktop notification not shown", "err", err)
				continue
			}
			shown = id
		}
	}
}

func webviewGPUPolicy(env string) application.WebviewGpuPolicy {
	switch parseGPUPolicy(env) {
	case gpuAlways:
		return application.WebviewGpuPolicyAlways
	case gpuOnDemand:
		return application.WebviewGpuPolicyOnDemand
	case gpuNever:
		return application.WebviewGpuPolicyNever
	}
	return application.WebviewGpuPolicyAlways // Wails' zero value
}
