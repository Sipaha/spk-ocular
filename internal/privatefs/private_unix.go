//go:build !windows

// Package privatefs creates files and directories accessible only to their owner.
package privatefs

import "os"

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func CreateTemp(dir, pattern string) (*os.File, error) { return os.CreateTemp(dir, pattern) }

func CreateNew(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}
