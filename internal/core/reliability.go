package core

// Reliability is a finite declaration snapshot, not a health or efficiency score.
// Values are whitelisted scalars; arbitrary manifest contents are never returned.
type Reliability struct {
	Ref             Ref                      `json:"ref"`
	ResourceVersion string                   `json:"resourceVersion"`
	CapturedAt      int64                    `json:"capturedAt"`
	Observations    []ReliabilityObservation `json:"observations"`
	PDBState        string                   `json:"pdbState"`
	HPAState        string                   `json:"hpaState"`
	Partial         bool                     `json:"partial"`
}
type ReliabilityObservation struct {
	Rule            string `json:"rule"`
	Version         int    `json:"version"`
	Source          Ref    `json:"source"`
	ResourceVersion string `json:"resourceVersion"`
	Field           string `json:"field"`
	Container       string `json:"container,omitempty"`
	Outcome         string `json:"outcome"`
	Severity        string `json:"severity"`
	Code            string `json:"code"`
	Value           string `json:"value,omitempty"`
	Reference       string `json:"reference"`
}
