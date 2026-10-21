package core

// ArgoSnapshot is bounded controller evidence, not a comparison made by Ocular
// against Git or a guarantee that remote destination resources are reachable.
type ArgoSnapshot struct {
	Servers    []Ref          `json:"servers"`
	Objects    []ArgoObject   `json:"objects"`
	Events     []ArgoEvent    `json:"events"`
	Coverage   []ArgoCoverage `json:"coverage"`
	Discovery  string         `json:"discovery"`
	Truncated  bool           `json:"truncated"`
	CapturedAt int64          `json:"capturedAt"`
}
type ArgoCoverage struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	State string `json:"state"`
}
type ArgoObject struct {
	Truncated            bool            `json:"truncated"`
	OperationMessage     string          `json:"operationMessage"`
	Ref                  Ref             `json:"ref"`
	Kind                 string          `json:"kind"`
	Project              string          `json:"project"`
	Sync                 string          `json:"sync"`
	Health               string          `json:"health"`
	Operation            string          `json:"operation"`
	Destination          string          `json:"destination"`
	DestinationNamespace string          `json:"destinationNamespace"`
	LocalDestination     bool            `json:"localDestination"`
	Sources              []ArgoSource    `json:"sources"`
	Conditions           []ArgoCondition `json:"conditions"`
	Resources            []ArgoResource  `json:"resources"`
	Generators           []string        `json:"generators"`
	Description          string          `json:"description"`
	ReconciledAt         int64           `json:"reconciledAt"`
	CreatedAt            int64           `json:"createdAt"`
}
type ArgoSource struct {
	Repository       string `json:"repository"`
	Path             string `json:"path"`
	Chart            string `json:"chart"`
	TargetRevision   string `json:"targetRevision"`
	ObservedRevision string `json:"observedRevision"`
}
type ArgoCondition struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// A reported resource has no UID in Argo's status. Ref is resolved only for a
// proven local destination with a fresh, scoped Kubernetes read on explicit use.
type ArgoResource struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Sync      string `json:"sync"`
	Health    string `json:"health"`
	Message   string `json:"message"`
}
type ArgoEvent struct {
	ID       string `json:"id"`
	Ref      Ref    `json:"ref"`
	Openable bool   `json:"openable"`
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Message  string `json:"message"`
	At       int64  `json:"at"`
	Count    int64  `json:"count"`
}
