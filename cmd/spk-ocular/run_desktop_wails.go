//go:build wails

package main

import (
	"context"

	"github.com/spk/spk-ocular/internal/appfiles"
	"github.com/spk/spk-ocular/internal/desktop"
)

func runDesktop(ctx context.Context) error {
	c, err := newCore(ctx, "desktop")
	if err != nil {
		return err
	}
	defer c.Close()
	return desktop.Run(ctx, desktop.Options{
		FrontendFS: frontendFS(),
		Service:    c.Service,
		Emitter:    c.Emitter,
		IconPNG:    appfiles.IconPNG,
	})
}
