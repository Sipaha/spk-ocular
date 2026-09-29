package main

import "runtime/debug"

// Go runtime memory policy, taken from spk-mm-client (its spike S4): the
// live heap of a UI backend is small, and the default GOGC=100 lets the heap
// the runtime keeps grow to about twice that between collections. GOGC=50
// trades a few more sub-millisecond collections for less Private_Dirty. The
// soft limit is a ceiling for spikes (a big cluster's initial lists): near
// it the runtime collects more often and slows down rather than crashes.
// Escape hatch: GOGC / GOMEMLIMIT in the environment win.
const (
	goGCPercent   = 50
	goMemoryLimit = 128 << 20
)

func tuneGoMemory(getenv func(string) string) {
	if getenv("GOGC") == "" {
		debug.SetGCPercent(goGCPercent)
	}
	if getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(goMemoryLimit)
	}
}
