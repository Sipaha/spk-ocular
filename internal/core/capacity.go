package core

// NodeCapacity is a namespace-scoped declaration ledger, never scheduler capacity.
type NodeCapacityRequest struct {
	Ref       Ref    `json:"ref"`
	Namespace string `json:"namespace"`
}
type CapacityQuantity struct {
	State string `json:"state"` // declared, unspecified or unknown
	Value string `json:"value,omitempty"`
}
type CapacityResources struct {
	CPU    CapacityQuantity `json:"cpu"`
	Memory CapacityQuantity `json:"memory"`
}
type CapacityPod struct {
	Ref             Ref               `json:"ref"`
	ResourceVersion string            `json:"resourceVersion"`
	Requests        CapacityResources `json:"requests"`
}
type NodeCapacity struct {
	Ref              Ref               `json:"ref"`
	Namespace        string            `json:"namespace"`
	ResourceVersion  string            `json:"resourceVersion"`
	CapturedAt       int64             `json:"capturedAt"`
	Allocatable      CapacityResources `json:"allocatable"`
	Requests         CapacityResources `json:"requests"`
	Pods             []CapacityPod     `json:"pods"`
	Coverage         string            `json:"coverage"`
	ExcludedTerminal int               `json:"excludedTerminal"`
	Unverified       int               `json:"unverified"`
}
