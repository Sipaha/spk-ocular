package desktop

import (
	"golang.org/x/sys/unix"
	"os"
)

// publishDownload atomically refuses every existing destination, including
// entries created during the transfer. Both paths use the chosen directory fd.
func publishDownload(parent *os.Root, _ string, old, name string) error {
	f, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Renameat2(int(f.Fd()), old, int(f.Fd()), name, unix.RENAME_NOREPLACE)
}
