//go:build wails && !linux

package desktop

// Windows and macOS don't use D-Bus: nothing to probe.
func sessionBusState() busState { return busOK }

func cutOffSessionBus() {}

// sendNotification: not on these systems (the window's title says it).
func sendNotification(string, string, uint32) (uint32, error) { return 0, nil }
