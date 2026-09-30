// Package paths resolves where spk-ocular keeps its data.
package paths

import (
	"os"
	"path/filepath"
)

// EnvHome overrides the data dir (tests, e2e, side-by-side dev instances).
const EnvHome = "SPK_OCULAR_HOME"

type Paths struct {
	DataDir string
	DBFile  string
	TmpDir  string
	// AgentSocket serves agent access (P14); AgentLock is held by the
	// instance serving it.
	AgentSocket string
	AgentLock   string
}

// Resolve returns ~/.spk/ocular (house convention shared with the other spk-*
// apps), overridable via SPK_OCULAR_HOME.
func Resolve() (Paths, error) {
	dir := os.Getenv(EnvHome)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".spk", "ocular")
	}
	return Paths{
		DataDir: dir,
		DBFile:  filepath.Join(dir, "ocular.db"),
		TmpDir:  filepath.Join(dir, "tmp"),

		AgentSocket: filepath.Join(dir, "agent.sock"),
		AgentLock:   filepath.Join(dir, "agent.sock.lock"),
	}, nil
}

// Ensure creates DataDir owner-only.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.DataDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(p.DataDir, 0o700)
}
