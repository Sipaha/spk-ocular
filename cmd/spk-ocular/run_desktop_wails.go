//go:build wails

package main

import (
	"context"
	"log/slog"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/appfiles"
	"github.com/spk/spk-ocular/internal/desktop"
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
	// truncates and buffers streams there). Log downloads use a native chooser.
	sh := streams.NewHandler(c.Service.Streams(), streams.HandlerOptions{
		AllowOrigin: desktop.PageOrigin,
		Classify:    api.StreamErrorClass,
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
