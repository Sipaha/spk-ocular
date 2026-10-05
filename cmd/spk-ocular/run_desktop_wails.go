//go:build wails

package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/appfiles"
	"github.com/spk/spk-ocular/internal/desktop"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/streams"
)

func runDesktop(ctx context.Context, o browserOpts) (err error) {
	closeLog, err := desktopLog()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			slog.Error("desktop stopped", "err", err)
		}
		closeLog()
	}()
	c, err := newCore(ctx, "desktop", o.TestSynthetic)
	if err != nil {
		return err
	}
	defer c.Close()
	if o.TestAPI {
		stop, err := startTestAPI(c)
		if err != nil {
			return err
		}
		defer stop()
	}
	// Logs stream from a loopback server, never through wails:// (WebKitGTK
	// truncates and buffers streams there); saving goes to Downloads (the
	// webview has no download manager).
	sh := streams.NewHandler(c.Service.Streams(), streams.HandlerOptions{
		AllowOrigin: desktop.PageOrigin,
		Classify:    api.StreamErrorClass,
		SaveDir:     func() (string, error) { return paths.Downloads(os.Getenv) },
	})
	lb := streams.NewLoopback(sh)
	defer func() { _ = lb.Close() }()
	c.Service.SetStreamBase(lb.Base)
	return desktop.Run(ctx, desktop.Options{
		FrontendFS: frontendFS(),
		Service:    c.Service,
		Emitter:    c.Emitter,
		IconPNG:    appfiles.IconPNG,
		DataDir:    c.Paths.DataDir,
	})
}
