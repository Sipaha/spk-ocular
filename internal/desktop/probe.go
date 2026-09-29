// Package desktop is the Wails shell: one window over the api.API service.
// No tray and no notifications — closing the window quits the app.
package desktop

import "time"

// busState is the outcome of probing the D-Bus session bus once at startup.
type busState int

const (
	busOK          busState = iota
	busUnreachable          // dial failed fast (no bus, bad address)
	busTimedOut             // hung bus: accepts the connection, never answers
)

// probeBus runs connect bounded by timeout. connect is typically a
// dbus.ConnectSessionBus()+Close(), which has no timeout of its own; on
// timeout its goroutine is abandoned (it holds nothing we need back).
func probeBus(connect func() error, timeout time.Duration) (busState, error) {
	done := make(chan error, 1)
	go func() { done <- connect() }()
	select {
	case err := <-done:
		if err != nil {
			return busUnreachable, err
		}
		return busOK, nil
	case <-time.After(timeout):
		return busTimedOut, nil
	}
}

// cutOffBusFor says whether to point DBUS_SESSION_BUS_ADDRESS at a dead
// address before GTK starts. GLib connects to the session bus with no timeout
// (GApplication registration inside g_application_run, a11y), so with a hung
// bus the window would never appear. Only Linux uses D-Bus.
func cutOffBusFor(goos string, bus busState) bool {
	return goos == "linux" && bus != busOK
}
