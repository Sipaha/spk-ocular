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
	// InstanceLabel and ChannelLabel name the levels (k8s: Pod, Container);
	// nil: the UI's generic words.
	InstanceLabel *Message `json:"instanceLabel,omitempty"`
	ChannelLabel  *Message `json:"channelLabel,omitempty"`
	// NoInstances says there is nowhere to run now (k8s: "No running
	// pods"); nil: the UI's generic words.
	NoInstances *Message `json:"noInstances,omitempty"`
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
	// ConfigHash of the target configuration the snapshot came from (never
	// sent to the UI, like Target.ConfigHash).
	ConfigHash string `json:"-"`
	// ConfigRev is its opaque stand-in (set by the API): the UI marks a
	// resource whose target's ConfigRev differs — reconfigured since.
	ConfigRev string `json:"configRev,omitempty"`
	Ref       Ref    `json:"ref"`
	Instance  string `json:"instance,omitempty"` // title of the pinned instance
	Channel   string `json:"channel,omitempty"`
	// Port: the remote port of a tunnel.
	Port int `json:"port,omitempty"`
	// Command: argv as run (empty = the interactive shell).
	Command []string `json:"command,omitempty"`
}

// ForwardPort is a port of an object that can be forwarded.
type ForwardPort struct {
	// Port is the number to forward (k8s: the Service port or the
	// container port).
	Port int    `json:"port"`
	Name string `json:"name,omitempty"`
	// Protocol: "TCP"; others are listed but not Supported.
	Protocol string `json:"protocol"`
	// Note: where it leads, human-readable ("→ 8080", "container web").
	Note string `json:"note,omitempty"`
	// Scheme is "http" or "https" when the port is known to speak it (its
	// name or application protocol says so): the UI offers "Open".
	Scheme    string `json:"scheme,omitempty"`
	Supported bool   `json:"supported"`
	// Reason: why it cannot be forwarded.
	Reason string `json:"reason,omitempty"`
}

// ForwardInfo says which ports of an object can be forwarded.
type ForwardInfo struct {
	Ports []ForwardPort `json:"ports"`
	// AnyPort: a port that is not listed can be forwarded too (k8s: a Pod).
	AnyPort bool `json:"anyPort,omitempty"`
	// Unsupported: nothing can be forwarded, and why (a selectorless
	// Service, an ExternalName).
	Unsupported string `json:"unsupported,omitempty"`
}
