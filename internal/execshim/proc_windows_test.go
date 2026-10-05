package execshim

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestWindowsPluginHasNoConsoleAndTimeoutKillsIt(t *testing.T) {
	cmd := exec.Command("cmd.exe")
	setChildAttrs(cmd)
	require.NotZero(t, cmd.SysProcAttr.CreationFlags&uint32(0x08000000))
	require.NotZero(t, cmd.SysProcAttr.CreationFlags&uint32(0x00000004))
	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := Main([]string{"--timeout", "500ms", "--", "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 600"}, strings.NewReader(""), &stdout, &stderr)
	require.Equal(t, 1, code)
	require.Contains(t, stderr.String(), "did not answer within 500ms")
	require.Less(t, time.Since(start), 10*time.Second)
}
