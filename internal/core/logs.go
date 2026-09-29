package core

// LogChannel is one thing whose logs can be chosen for an object (k8s: a
// container of a pod or of a workload's pods; compose: a service's
// container). Note marks special ones ("init", "sidecar", "ephemeral").
type LogChannel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
}

// LogInfo says what logs an object has.
type LogInfo struct {
	Channels       []LogChannel `json:"channels"`
	DefaultChannel string       `json:"defaultChannel"`
	// Aggregate: the object is a group of sources (a workload's pods).
	Aggregate bool `json:"aggregate"`
	// Previous: logs of the previous (last terminated) instance exist as a
	// concept for this object (one pod).
	Previous bool `json:"previous"`
}
