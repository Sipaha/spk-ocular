package kubernetes

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
	"testing"
	"time"
)

func cpuTime(t *testing.T) time.Duration {
	var created, exited, kernel, user windows.Filetime
	require.NoError(t, windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user))
	ticks := (uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) + (uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))
	return time.Duration(ticks * 100)
}
