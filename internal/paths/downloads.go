package paths

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Downloads is the user's download directory: XDG_DOWNLOAD_DIR from the
// environment or ~/.config/user-dirs.dirs, else ~/Downloads. It is where the
// desktop app saves files (WebKitGTK has no download manager).
func Downloads(getenv func(string) string) (string, error) {
	home := getenv("HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = h
	}
	if d := expandXDG(getenv("XDG_DOWNLOAD_DIR"), home); d != "" {
		return d, nil
	}
	// An explicit XDG directory wins for isolated profiles on every platform.
	if getenv("XDG_CONFIG_HOME") == "" && (getenv("HOME") == "" || getenv("HOME") == getenv("USERPROFILE")) {
		if dir, err := nativeDownloads(); err != nil {
			return "", err
		} else if dir != "" {
			return dir, nil
		}
	}
	cfg := getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	if f, err := os.Open(filepath.Join(cfg, "user-dirs.dirs")); err == nil {
		defer func() { _ = f.Close() }()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
			if !ok || k != "XDG_DOWNLOAD_DIR" {
				continue
			}
			if uq, err := strconv.Unquote(v); err == nil {
				v = uq
			}
			if d := expandXDG(v, home); d != "" {
				return d, nil
			}
		}
	}
	return filepath.Join(home, "Downloads"), nil
}

// expandXDG resolves "$HOME/x" / absolute paths; anything else is ignored.
// "$HOME/" alone means the home directory, which xdg-user-dirs uses to say
// "disabled" — the caller then falls back to ~/Downloads.
func expandXDG(v, home string) string {
	v = strings.TrimSpace(v)
	switch {
	case strings.HasPrefix(v, "$HOME/"):
		v = filepath.Join(home, strings.TrimPrefix(v, "$HOME/"))
	case !filepath.IsAbs(v):
		return ""
	}
	if filepath.Clean(v) == filepath.Clean(home) {
		return ""
	}
	return filepath.Clean(v)
}
