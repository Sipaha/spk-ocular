package main

import (
	"bytes"
	"context"
	"math"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootDispatchesModes(t *testing.T) {
	var gotBrowser *browserOpts
	desktop := false
	var gotDesktop browserOpts
	r := runners{
		browser: func(_ context.Context, o browserOpts) error { gotBrowser = &o; return nil },
		desktop: func(_ context.Context, o browserOpts) error { desktop, gotDesktop = true, o; return nil },
	}

	cmd := newRootCmd(r)
	cmd.SetArgs([]string{})
	require.NoError(t, cmd.Execute())
	assert.True(t, desktop, "no flags: desktop window")
	assert.Equal(t, browserOpts{Port: 5190}, gotDesktop)

	cmd = newRootCmd(r)
	cmd.SetArgs([]string{"--test-api"})
	require.NoError(t, cmd.Execute())
	assert.True(t, gotDesktop.TestAPI, "the desktop app has test routes too")

	cmd = newRootCmd(r)
	cmd.SetArgs([]string{"--browser", "--port", "6001", "--test-api"})
	require.NoError(t, cmd.Execute())
	require.NotNil(t, gotBrowser)
	assert.Equal(t, browserOpts{Port: 6001, TestAPI: true}, *gotBrowser)

	cmd = newRootCmd(r)
	cmd.SetArgs([]string{"--browser", "--test-synthetic"})
	assert.ErrorContains(t, cmd.Execute(), "needs --test-api", "the synthetic provider is test-only")
}

func TestVersionCommand(t *testing.T) {
	cmd := newRootCmd(runners{})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	require.NoError(t, cmd.Execute())
	assert.Equal(t, "spk-ocular "+version+"\n", out.String())
}

func TestTuneGoMemoryRespectsEnvironment(t *testing.T) {
	orig := debug.SetGCPercent(100)
	origLimit := debug.SetMemoryLimit(math.MaxInt64)
	t.Cleanup(func() { debug.SetGCPercent(orig); debug.SetMemoryLimit(origLimit) })

	tuneGoMemory(func(string) string { return "" })
	assert.Equal(t, goGCPercent, debug.SetGCPercent(100))
	assert.Equal(t, int64(goMemoryLimit), debug.SetMemoryLimit(math.MaxInt64))

	// GOGC / GOMEMLIMIT set by the user: leave the runtime's values alone.
	tuneGoMemory(func(k string) string { return map[string]string{"GOGC": "100", "GOMEMLIMIT": "1GiB"}[k] })
	assert.Equal(t, 100, debug.SetGCPercent(100))
	assert.Equal(t, int64(math.MaxInt64), debug.SetMemoryLimit(math.MaxInt64))
}
