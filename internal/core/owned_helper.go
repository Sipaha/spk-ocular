package core

// OwnedHelper is non-secret recovery metadata, persisted before Pod creation.
// NamespaceUID and NodeUID bind ownership independently of mutable kubeconfigs.
type OwnedHelper struct {
	ID           string `json:"id"`
	Provider     string `json:"provider"`
	Target       string `json:"target"`
	Namespace    string `json:"namespace"`
	NamespaceUID string `json:"namespaceUID"`
	Name         string `json:"name"`
	UID          string `json:"uid,omitempty"`
	NodeName     string `json:"nodeName"`
	NodeUID      string `json:"nodeUID"`
	CreatedAt    int64  `json:"createdAt"`
}
