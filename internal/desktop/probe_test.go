package desktop

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestProbeBus(t *testing.T) {
	st, err := probeBus(func() error { return nil }, 50*time.Millisecond)
	assert.Equal(t, busOK, st)
	assert.NoError(t, err)

	boom := errors.New("no such file")
	st, err = probeBus(func() error { return boom }, 50*time.Millisecond)
	assert.Equal(t, busUnreachable, st)
	assert.ErrorIs(t, err, boom)

	block := make(chan struct{})
	defer close(block)
	start := time.Now()
	st, err = probeBus(func() error { <-block; return nil }, 50*time.Millisecond)
	assert.Equal(t, busTimedOut, st, "a hung bus (accepts, never answers)")
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
}

func TestCutOffBusFor(t *testing.T) {
	assert.False(t, cutOffBusFor("linux", busOK))
	assert.True(t, cutOffBusFor("linux", busUnreachable))
	assert.True(t, cutOffBusFor("linux", busTimedOut))
	assert.False(t, cutOffBusFor("windows", busTimedOut))
	assert.False(t, cutOffBusFor("darwin", busUnreachable))
}

func TestParseGPUPolicy(t *testing.T) {
	assert.Equal(t, gpuAlways, parseGPUPolicy("always"))
	assert.Equal(t, gpuOnDemand, parseGPUPolicy(" OnDemand "))
	assert.Equal(t, gpuNever, parseGPUPolicy("off"))
	assert.Equal(t, gpuDefault, parseGPUPolicy(""))
	assert.Equal(t, gpuDefault, parseGPUPolicy("bogus"))
}
