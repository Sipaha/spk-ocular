package desktop

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishDownload(parent *os.Root, _ string, old, name string) error {
	f, err := parent.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.RenameatxNp(int(f.Fd()), old, int(f.Fd()), name, unix.RENAME_EXCL)
}
