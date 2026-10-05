//go:build !windows

package paths

import "path/filepath"

// AgentEndpoint is a Unix socket on Linux and macOS.
func AgentEndpoint(dir string) string { return filepath.Join(dir, "agent.sock") }
