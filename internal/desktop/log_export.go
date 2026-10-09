package desktop

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spk/spk-ocular/internal/privatefs"
)

// writeLogExport stages an owner-only UTF-8 file beside the chosen destination.
// The native chooser authorizes replacement; a failed write keeps the old file.
func writeLogExport(destination, text string) error {
	f, err := privatefs.CreateTemp(filepath.Dir(destination), ".ocular-log-download-*")
	if err != nil {
		return err
	}
	staged := f.Name()
	defer func() { _ = f.Close(); _ = os.Remove(staged) }()
	if _, err = io.Copy(f, strings.NewReader(text)); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(staged, destination)
}
