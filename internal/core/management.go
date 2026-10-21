package core

// ManagementContext contains only self-tracking hints from the exact object
// read for a review. It is UI-only advice, not verified controller ownership or
// a new write precondition. Controller observations require a separate explicit
// UI read; preparing a review never inventories controllers for this context.
type ManagementContext struct {
	Ref          Ref           `json:"ref"`
	Version      string        `json:"version"`
	CapturedAt   int64         `json:"capturedAt"`
	ArgoTracking *ArgoTracking `json:"argoTracking,omitempty"`
	FluxTracking *FluxTracking `json:"fluxTracking,omitempty"`
	Truncated    bool          `json:"truncated"`
}
