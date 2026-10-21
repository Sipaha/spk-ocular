package core

// Diagnosis is a finite observation, not a continuously reconciled verdict.
type Diagnosis struct {
	Ref        Ref                 `json:"ref"`
	CapturedAt int64               `json:"capturedAt"`
	Evidence   []DiagnosisEvidence `json:"evidence"`
	Coverage   []DiagnosisCoverage `json:"coverage"`
	Truncated  bool                `json:"truncated"`
}

type DiagnosisEvidence struct {
	Ref       Ref         `json:"ref"`
	Source    Ref         `json:"source"`
	Rule      string      `json:"rule"`
	Field     string      `json:"field"`
	State     HealthState `json:"state"`
	Reason    string      `json:"reason"`
	Message   string      `json:"message,omitempty"`
	At        int64       `json:"at,omitempty"`
	Related   *Ref        `json:"related,omitempty"`
	Container string      `json:"container,omitempty"`
	Previous  bool        `json:"previous,omitempty"`
}

// State is ready, partial, not_covered, keys_unchecked or a provider error class.
// A failed read never proves that a dependency is absent.
type DiagnosisCoverage struct {
	Source string `json:"source"`
	State  string `json:"state"`
}
