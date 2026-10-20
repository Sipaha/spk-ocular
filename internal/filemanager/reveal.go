// Package filemanager reveals local items without opening their contents.
package filemanager

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Reveal selects the item in its parent directory. Linux requires FileManager1;
// opening only the parent would silently lose the requested selection.
// https://wiki.freedesktop.org/www/Specifications/file-manager-interface/
func Reveal(ctx context.Context, path string) error {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return errors.New("an absolute local item path is required")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		if address == "" {
			return errors.New("file selection requires a session file manager")
		}
		conn, err := dbus.Connect(address, dbus.WithContext(ctx))
		if err != nil {
			return errors.New("could not connect to the file manager")
		}
		defer func() { _ = conn.Close() }()
		uri := (&url.URL{Scheme: "file", Path: path}).String()
		if err = conn.Object("org.freedesktop.FileManager1", "/org/freedesktop/FileManager1").CallWithContext(ctx, "org.freedesktop.FileManager1.ShowItems", 0, []string{uri}, "").Err; err != nil {
			return errors.New("could not select the item in the file manager")
		}
		return nil
	}
	name, args := Command(runtime.GOOS, path)
	if name == "" {
		return errors.New("file selection is unavailable on this platform")
	}
	// Explorer can remain the file-manager process itself after dispatch.
	// Preserve its lifetime and do not interpret its later exit code as selection.
	if runtime.GOOS == "windows" {
		return dispatchSelection(ctx, name, args)
	}
	if err := exec.CommandContext(ctx, name, args...).Run(); err != nil {
		return errors.New("could not select the item in the file manager")
	}
	return nil
}

// Command keeps paths as a single argument on platforms without FileManager1.
func Command(platform, path string) (string, []string) {
	switch platform {
	case "darwin":
		return "open", []string{"-R", path}
	case "windows":
		return "explorer.exe", []string{"/select," + path}
	default:
		return "", nil
	}
}

func dispatchSelection(ctx context.Context, name string, args []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := exec.Command(name, args...)
	if err := command.Start(); err != nil {
		return errors.New("could not start the file manager")
	}
	go func() { _ = command.Wait() }()
	return nil
}
