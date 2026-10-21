package core

// FluxResourceKinds is the finite set of controller APIs supported by the UI.
// API discovery selects the served version; an API group name alone is not detection.
var FluxResourceKinds = []struct{ Group, Resource, Kind, Section string }{
	{"source.toolkit.fluxcd.io", "gitrepositories", "GitRepository", "Sources"},
	{"source.toolkit.fluxcd.io", "ocirepositories", "OCIRepository", "Sources"},
	{"source.toolkit.fluxcd.io", "helmrepositories", "HelmRepository", "Sources"},
	{"source.toolkit.fluxcd.io", "helmcharts", "HelmChart", "Sources"},
	{"source.toolkit.fluxcd.io", "buckets", "Bucket", "Sources"},
	{"source.toolkit.fluxcd.io", "externalartifacts", "ExternalArtifact", "Sources"},
	{"kustomize.toolkit.fluxcd.io", "kustomizations", "Kustomization", "Workloads"},
	{"helm.toolkit.fluxcd.io", "helmreleases", "HelmRelease", "Workloads"},
	{"image.toolkit.fluxcd.io", "imagerepositories", "ImageRepository", "Image automation"},
	{"image.toolkit.fluxcd.io", "imagepolicies", "ImagePolicy", "Image automation"},
	{"image.toolkit.fluxcd.io", "imageupdateautomations", "ImageUpdateAutomation", "Image automation"},
	{"notification.toolkit.fluxcd.io", "alerts", "Alert", "Notifications"},
	{"notification.toolkit.fluxcd.io", "providers", "Provider", "Notifications"},
	{"notification.toolkit.fluxcd.io", "receivers", "Receiver", "Notifications"},
}

// FluxSnapshot contains bounded controller observations, never chart values,
// decryption material, webhook addresses/tokens or Secret contents.
type FluxManagerHint struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	UID       string `json:"uid,omitempty"`
}
type FluxTracking struct {
	Group      string            `json:"group"`
	Kind       string            `json:"kind"`
	Candidates []FluxManagerHint `json:"candidates"`
}
type FluxSnapshot struct {
	Objects    []FluxObject   `json:"objects"`
	Events     []ArgoEvent    `json:"events"`
	Coverage   []ArgoCoverage `json:"coverage"`
	Discovery  string         `json:"discovery"`
	Truncated  bool           `json:"truncated"`
	CapturedAt int64          `json:"capturedAt"`
}
type FluxObject struct {
	Ref                Ref             `json:"ref"`
	Kind               string          `json:"kind"`
	Section            string          `json:"section"`
	State              string          `json:"state"`
	Message            string          `json:"message"`
	Generation         int64           `json:"generation"`
	ObservedGeneration int64           `json:"observedGeneration"`
	Conditions         []FluxCondition `json:"conditions"`
	URL                string          `json:"url"`
	Path               string          `json:"path"`
	Revision           string          `json:"revision"`
	Links              []FluxLink      `json:"links"`
	Inventory          []FluxResource  `json:"inventory"`
	LocalDestination   bool            `json:"localDestination"`
	CreatedAt          int64           `json:"createdAt"`
	Truncated          bool            `json:"truncated"`
}
type FluxCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message"`
	ObservedGeneration int64  `json:"observedGeneration"`
}
type FluxLink struct {
	Relation  string `json:"relation"`
	Kind      string `json:"kind"`
	Group     string `json:"group"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
type FluxResource struct {
	Group     string `json:"group"`
	Kind      string `json:"kind"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}
