package core

// OwnedHelper is non-secret recovery metadata persisted before helper creation.
// Empty Kind denotes the legacy Node-owned Pod; krr-job is an independent Job.
type OwnedHelper struct {
	ContractSHA       string `json:"contractSHA,omitempty"`
	Kind              string `json:"kind,omitempty"`
	ServiceAccount    string `json:"serviceAccount,omitempty"`
	ServiceAccountUID string `json:"serviceAccountUID,omitempty"`
	ID                string `json:"id"`
	Provider          string `json:"provider"`
	Target            string `json:"target"`
	Namespace         string `json:"namespace"`
	NamespaceUID      string `json:"namespaceUID"`
	Name              string `json:"name"`
	UID               string `json:"uid,omitempty"`
	NodeName          string `json:"nodeName"`
	NodeUID           string `json:"nodeUID"`
	CreatedAt         int64  `json:"createdAt"`
}
