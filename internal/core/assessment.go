package core

// NamespaceAssessment is an explicitly requested, bounded overview. Counts are
// observations from separate UID-pinned reads, never a namespace health score.
type NamespaceAssessmentRequest struct {
	Provider  string `json:"provider"`
	Target    string `json:"target"`
	Namespace string `json:"namespace"`
	Check     string `json:"check"`
}
type AssessmentCounts struct {
	Observed int `json:"observed"`
	Review   int `json:"review"`
	Unknown  int `json:"unknown"`
	Excluded int `json:"excluded"`
}
type AssessmentHighlight struct {
	SourceState string `json:"sourceState,omitempty"`
	Freshness   string `json:"freshness,omitempty"`
	Field       string `json:"field"`
	Container   string `json:"container,omitempty"`
	Code        string `json:"code,omitempty"`
	Value       string `json:"value,omitempty"`
}
type AssessmentResource struct {
	Ref        Ref                   `json:"ref"`
	State      string                `json:"state"`
	Counts     AssessmentCounts      `json:"counts"`
	Highlights []AssessmentHighlight `json:"highlights"`
}
type AssessmentCoverage struct {
	Kind  string `json:"kind"`
	State string `json:"state"`
}
type NamespaceAssessment struct {
	Namespace  string               `json:"namespace"`
	Check      string               `json:"check"`
	CapturedAt int64                `json:"capturedAt"`
	Resources  []AssessmentResource `json:"resources"`
	Coverage   []AssessmentCoverage `json:"coverage"`
	Partial    bool                 `json:"partial"`
}
