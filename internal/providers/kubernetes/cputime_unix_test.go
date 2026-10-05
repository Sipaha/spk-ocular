//go:build !windows

package kubernetes

import (
	"github.com/stretchr/testify/require"
	"syscall"
	"testing"
	"time"
)

func cpuTime(t *testing.T) time.Duration {
	var u syscall.Rusage
	require.NoError(t, syscall.Getrusage(syscall.RUSAGE_SELF, &u))
	return time.Duration(u.Utime.Nano() + u.Stime.Nano())
}
