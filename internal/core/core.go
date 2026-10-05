// Package core holds the provider-agnostic model the UI works with. Nothing
// here may know about pods, namespaces or containers: Kubernetes is one
// provider alongside Docker Compose (docs/architecture.md).
package core

// Target is one thing a provider can connect to: a kube context, a Docker
// context, an SSH host. ID is stable and unique within the provider.
type Target struct {
	Provider string `json:"provider"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	// Subtitle is a short secondary line (the cluster/host), may be empty.
	Subtitle string `json:"subtitle,omitempty"`
	// Current marks the provider's own default (kubeconfig current-context).
	Current bool `json:"current,omitempty"`
	// Details are human-readable, non-secret facts in display order.
	Details []Detail `json:"details,omitempty"`
	// DefaultScope is the scope a first visit shows (k8s: the context's
	// namespace); empty: all scopes.
	DefaultScope string `json:"defaultScope,omitempty"`
	// ConfigHash identifies the configuration the target resolves to; an
	// open session built from another hash is stale. Never sent to the UI:
	// it covers credentials.
	ConfigHash string `json:"-"`
	// ConfigRev is the UI's opaque stand-in for ConfigHash (set by the API,
	// keyed per process): equal revisions = the same configuration.
	ConfigRev string `json:"configRev,omitempty"`
	// Open: the target has a live session (its caches and watches), set by
	// the API (P18).
	Open bool `json:"open,omitempty"`
	// Connection is this process's explicit UI connection attempt.
	Connection *ConnectionStatus `json:"connection,omitempty"`
	// Identity says what the target points at (k8s: the server, a digest
	// of the CA it trusts, the auth-info it names; Docker: the endpoint),
	// never a credential: agents' grants are bound to it, so a context
	// pointed at another cluster suspends them while a rotated token does
	// not. Readable, not secret; the agent API sends it itself.
	Identity string `json:"-"`
}

type ConnectionStatus struct {
	ID             uint64 `json:"id"`
	State          string `json:"state"`
	Phase          string `json:"phase"`
	Attempt        int    `json:"attempt"`
	MaxAttempts    int    `json:"maxAttempts"`
	StartedAt      int64  `json:"startedAt"`
	AttemptStarted int64  `json:"attemptStarted"`
	FinishedAt     int64  `json:"finishedAt,omitempty"`
	RetryAt        int64  `json:"retryAt,omitempty"`
	ErrorClass     string `json:"errorClass,omitempty"`
	Error          string `json:"error,omitempty"`
}

// Detail is one labelled fact. Key is stable (for UI translation and tests),
// Value is shown as-is.
type Detail struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Problem is a non-fatal discovery issue (an unreadable kubeconfig file):
// the rest of the targets are still usable.
type Problem struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}
