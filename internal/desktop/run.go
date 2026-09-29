//go:build wails

package desktop

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/api/transport"
	"github.com/spk/spk-ocular/internal/events"
)

type Options struct {
	FrontendFS fs.FS
	Service    api.API
	Emitter    *events.Emitter
	IconPNG    []byte
}

// Run starts the Wails loop with one window. Closing the window quits the
// app (Wails quits on the last window closed); so does ctx cancellation.
func Run(ctx context.Context, o Options) error {
	var shutDown atomic.Bool
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if cutOffBusFor(runtime.GOOS, sessionBusState()) {
		cutOffSessionBus() // before application.New() initializes GTK
	}

	app := application.New(application.Options{
		// Warn: Info logs every asset request. Never Debug in a build that
		// ships: Wails logs binding results there.
		LogLevel:    slog.LevelWarn,
		Name:        "spk-ocular",
		Description: "Lightweight local infrastructure viewer",
		Icon:        o.IconPNG,
		Services:    []application.Service{application.NewService(transport.NewAPI(o.Service))},
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(o.FrontendFS)},
		OnShutdown: func() { // on the GTK main thread
			shutDown.Store(true)
			cancel()
		},
	})

	// Relay core events to the page (Wails events; payload is the one arg).
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
				}
			}
		}
	}()

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "SPK Ocular",
		Width:  1280,
		Height: 820,
		// Matches --color-app in web/src/index.css: no light flash before
		// the webview paints.
		BackgroundColour: application.NewRGBA(30, 31, 34, 255),
		URL:              "/",
		DevToolsEnabled:  devToolsEnabled,
		Linux:            application.LinuxWindow{WebviewGpuPolicy: webviewGPUPolicy(os.Getenv("SPK_OCULAR_GPU"))},
	})

	go func() {
		<-ctx.Done()
		if !shutDown.Load() { // the caller's ctx ended (SIGINT); not our own shutdown
			app.Quit()
		}
	}()
	return app.Run()
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
