package paths

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// AgentEndpoint gives each absolute profile path a separate, stable local pipe.
// The listener's DACL limits access to the current user, including pipe creation.
func AgentEndpoint(dir string) string {
	absolute, err := filepath.Abs(dir)
	if err != nil {
		absolute = filepath.Clean(dir)
	}
	digest := sha256.Sum256([]byte(strings.ToLower(absolute)))
	return fmt.Sprintf(`\\.\pipe\spk-ocular-%x`, digest[:16])
}
