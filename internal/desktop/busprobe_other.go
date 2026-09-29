//go:build wails && !linux

package desktop

// Windows and macOS don't use D-Bus: nothing to probe.
func sessionBusState() busState { return busOK }

func cutOffSessionBus() {}
