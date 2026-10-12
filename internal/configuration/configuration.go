// Package configuration defines the UI-only Kubernetes configuration contract
// without depending on the provider implementation or Kubernetes SDK.
package configuration

const KubernetesProviderID = "kubernetes"

// ConfigRequest is UI-only. Passwords and YAML must never enter logs or preferences.
type ConfigRequest struct {
	TargetRevision  string   `json:"targetRevision,omitempty"`
	Target          string   `json:"target,omitempty"`
	ContentRevision string   `json:"contentRevision,omitempty"`
	Command         string   `json:"command"`
	Paths           []string `json:"paths,omitempty"`
	Name            string   `json:"name,omitempty"`
	YAML            string   `json:"yaml,omitempty"`
	Password        string   `json:"password,omitempty"`
	ID              string   `json:"id,omitempty"`
	Expect          string   `json:"expect,omitempty"`
}
type ConfigCandidate struct {
	Path     string   `json:"path"`
	Contexts []string `json:"contexts"`
	Selected bool     `json:"selected"`
	Problem  string   `json:"problem,omitempty"`
}
type ConfigEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Path     string `json:"path,omitempty"`
	Revision string `json:"revision"`
}
type ConfigState struct {
	ResetRevision   string `json:"resetRevision,omitempty"`
	TargetName      string `json:"targetName,omitempty"`
	TargetRevision  string `json:"targetRevision,omitempty"`
	EntryID         string `json:"entryId,omitempty"`
	ContentRevision string `json:"contentRevision,omitempty"`
	// YAML exists only in the explicit inspect response, never in status or persistence.
	YAML        string            `json:"yaml,omitempty"`
	Initialized bool              `json:"initialized"`
	Encrypted   bool              `json:"encrypted"`
	Locked      bool              `json:"locked"`
	Candidates  []ConfigCandidate `json:"candidates"`
	Entries     []ConfigEntry     `json:"entries"`
}

// Source manages explicitly registered Kubernetes configurations. Implementations
// must preserve revision checks and never return YAML outside explicit inspection.
type Source interface {
	Configurations(ConfigRequest) (ConfigState, error)
	ConfigurationFile(ConfigRequest) (string, error)
}
