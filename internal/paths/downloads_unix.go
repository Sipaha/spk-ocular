//go:build !windows

package paths

func nativeDownloads() (string, error) { return "", nil }
