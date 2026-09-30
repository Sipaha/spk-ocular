//go:build wails && linux

package desktop

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

// dbusProbeTimeout bounds the one startup probe of the session bus.
const dbusProbeTimeout = 2 * time.Second

// deadBusAddress can never be connected to (a path under /dev/null), so every
// D-Bus client fails immediately instead of waiting on a hung bus.
const deadBusAddress = "unix:path=/dev/null/spk-ocular-dbus-disabled"

// sessionBusState probes the D-Bus session bus once, bounded by
// dbusProbeTimeout. A missing or hung bus (headless box, broken user session,
// sandbox) must not keep the window from appearing.
func sessionBusState() busState {
	st, err := probeBus(func() error {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			return err
		}
		return conn.Close()
	}, dbusProbeTimeout)
	switch st {
	case busUnreachable:
		slog.Warn("D-Bus session bus unreachable: cutting it off for GTK", "err", err)
	case busTimedOut:
		slog.Warn("D-Bus session bus not answering: cutting it off for GTK", "timeout", dbusProbeTimeout)
	}
	return st
}

// cutOffSessionBus makes every later D-Bus client in this process (GLib's
// GApplication registration, a11y, Wails' theme monitor) and in WebKit's
// child processes fail fast instead of blocking on an unusable bus.
func cutOffSessionBus() {
	if err := os.Setenv("DBUS_SESSION_BUS_ADDRESS", deadBusAddress); err != nil {
		slog.Warn("could not cut off the D-Bus session bus", "err", err)
	}
}

// notifyTimeout bounds one desktop notification (the bus answered the
// startup probe, but may hang later).
const notifyTimeout = 2 * time.Second

// sendNotification shows a desktop notification (org.freedesktop.
// Notifications), replacing the one with id replaces (0: none); it returns
// the new one's id. The window is never raised.
func sendNotification(summary, body string, replaces uint32) (uint32, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return 0, err
	}
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	var id uint32
	err = conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").
		CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
			"SPK Ocular", replaces, "", summary, body, []string{}, map[string]dbus.Variant{}, int32(-1)).
		Store(&id)
	return id, err
}
