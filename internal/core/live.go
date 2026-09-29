package core

// ExecChannel is something a command can run in (k8s: a container).
type ExecChannel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Note  string `json:"note,omitempty"` // "init", "sidecar", "ephemeral"
	// Running: a command can be started now; otherwise State says why not.
	Running bool   `json:"running"`
	State   string `json:"state,omitempty"`
}

// ExecInstance is one concrete thing an object runs as (k8s: a pod of a
// workload, or the pod itself).
type ExecInstance struct {
	ID             string        `json:"id"`
	Title          string        `json:"title"`
	Ready          bool          `json:"ready"`
	Channels       []ExecChannel `json:"channels"`
	DefaultChannel string        `json:"defaultChannel"`
}

// ExecInfo says where a command can run for an object.
type ExecInfo struct {
	Instances       []ExecInstance `json:"instances"`
	DefaultInstance string         `json:"defaultInstance"`
}

// LiveTarget describes what a live resource (terminal, tunnel) is
// connected to, as captured when it was opened: the target may since have
// been deselected or reconfigured.
type LiveTarget struct {
	Provider    string `json:"provider"`
	Target      string `json:"target"`
	TargetTitle string `json:"targetTitle"`
	// Endpoint is where the connection goes (k8s: the API server host),
	// never credentials.
	Endpoint string `json:"endpoint,omitempty"`
	// ConfigHash of the target configuration the snapshot came from: the UI
	// marks a resource whose target has been reconfigured since.
	ConfigHash string `json:"configHash,omitempty"`
	Ref        Ref    `json:"ref"`
	Instance   string `json:"instance,omitempty"` // title of the pinned instance
	Channel    string `json:"channel,omitempty"`
	// Command: argv as run (empty = the interactive shell).
	Command []string `json:"command,omitempty"`
}
