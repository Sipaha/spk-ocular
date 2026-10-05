//go:build wails && !windows

package main

func desktopLog() (func(), error) { return func() {}, nil }
