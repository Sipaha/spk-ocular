package desktop

import (
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

func publishDownload(_ *os.Root, directory, old, name string) error {
	from, err := windows.UTF16PtrFromString(filepath.Join(directory, old))
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(filepath.Join(directory, name))
	if err != nil {
		return err
	}
	// No MOVEFILE_REPLACE_EXISTING: an existing destination is always an error.
	return windows.MoveFileEx(from, to, 0)
}
