//go:build wails

package main

import (
	"context"
	"os"

	"github.com/spk/spk-ocular/internal/api"
	"github.com/spk/spk-ocular/internal/appfiles"
	"github.com/spk/spk-ocular/internal/desktop"
	"github.com/spk/spk-ocular/internal/paths"
	"github.com/spk/spk-ocular/internal/streams"
)

func runDesktop(ctx context.Context) error {
	c, err := newCore(ctx, "desktop", false)
	if err != nil {
		return err
	}
	defer c.Close()
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
	})
}
