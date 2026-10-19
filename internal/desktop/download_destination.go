package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// publishUniqueDownload atomically claims a free name without replacing files,
// directories or symlinks, including destinations created during the transfer.
func publishUniqueDownload(directory, temporary, name string) (string, error) {
	parent, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for n := 0; n < 10000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		err = publishDownload(parent, directory, temporary, candidate)
		if err == nil {
			return filepath.Join(directory, candidate), nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("too many existing download names")
}
