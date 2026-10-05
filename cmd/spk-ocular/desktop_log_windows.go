//go:build wails && windows

package main

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/spk/spk-ocular/internal/paths"
)

// A GUI-subsystem executable has no console. Preserve startup and webview
// diagnostics in the protected application profile, as the launcher does.
func desktopLog() (func(), error) {
	p, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	if err = p.Ensure(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(p.DataDir, "desktop.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return func() { slog.SetDefault(previous); _ = f.Close() }, nil
}
