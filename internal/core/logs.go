package core

// LogChannel is one thing whose logs can be chosen for an object (k8s: a
// container of a pod or of a workload's pods; compose: a service's
// container). Note marks special ones ("init", "sidecar", "ephemeral").
type LogChannel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"`
}

// LogInstance identifies a concrete source offered by an aggregate object.
type LogInstance struct {
	Ref   Ref    `json:"ref"`
	Title string `json:"title"`
}

// LogInfo says what logs an object has.
type LogInfo struct {
	// SelectChannels distinguishes concrete containers from stdout/stderr streams.
	SelectChannels    bool          `json:"selectChannels,omitempty"`
	InstanceLabel     *Message      `json:"instanceLabel,omitempty"`
	AllInstancesLabel *Message      `json:"allInstancesLabel,omitempty"`
	Instances         []LogInstance `json:"instances,omitempty"`
	Channels          []LogChannel  `json:"channels"`
	DefaultChannel    string        `json:"defaultChannel"`
	// Aggregate: the object is a group of sources (a workload's pods).
	Aggregate bool `json:"aggregate"`
	// Previous: logs of the previous (last terminated) instance exist as a
	// concept for this object (one pod).
	Previous bool `json:"previous"`
	// ChannelLabel names a channel (k8s: Container) and AllChannelsLabel
	// the choice of all of them; nil: the UI's generic words.
	ChannelLabel     *Message `json:"channelLabel,omitempty"`
	AllChannelsLabel *Message `json:"allChannelsLabel,omitempty"`
}
