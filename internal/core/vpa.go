package core

// VPAInspection projects existing external recommendations, never a sizing plan.
type VPAInspection struct {
	Ref             Ref              `json:"ref"`
	ResourceVersion string           `json:"resourceVersion"`
	CapturedAt      int64            `json:"capturedAt"`
	State           string           `json:"state"`
	HPAState        string           `json:"hpaState"`
	Sources         []VPASource      `json:"sources"`
	Declarations    []VPADeclaration `json:"declarations"`
	HPAMetrics      []VPAHPAMetric   `json:"hpaMetrics"`
}
type VPAResources struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}
type VPADeclaration struct {
	Container string       `json:"container"`
	Requests  VPAResources `json:"requests"`
	Limits    VPAResources `json:"limits"`
}
type VPARecommendation struct {
	Container      string       `json:"container"`
	Target         VPAResources `json:"target"`
	LowerBound     VPAResources `json:"lowerBound"`
	UpperBound     VPAResources `json:"upperBound"`
	UncappedTarget VPAResources `json:"uncappedTarget"`
}
type VPAPolicy struct {
	Container                string       `json:"container"`
	Mode                     string       `json:"mode"`
	ControlledValues         string       `json:"controlledValues"`
	ControlledResourcesKnown bool         `json:"controlledResourcesKnown"`
	ControlledResources      []string     `json:"controlledResources"`
	MinAllowed               VPAResources `json:"minAllowed"`
	MaxAllowed               VPAResources `json:"maxAllowed"`
}
type VPACondition struct {
	Type           string `json:"type"`
	Status         string `json:"status"`
	TransitionTime string `json:"transitionTime,omitempty"`
	Freshness      string `json:"freshness"`
}
type VPASource struct {
	Ref             Ref                 `json:"ref"`
	ResourceVersion string              `json:"resourceVersion"`
	APIVersion      string              `json:"apiVersion"`
	Recommender     string              `json:"recommender,omitempty"`
	UpdateMode      string              `json:"updateMode"`
	State           string              `json:"state"`
	Freshness       string              `json:"freshness"`
	Partial         bool                `json:"partial"`
	Conditions      []VPACondition      `json:"conditions"`
	Recommendations []VPARecommendation `json:"recommendations"`
	Policies        []VPAPolicy         `json:"policies"`
}
type VPAHPAMetric struct {
	Ref             Ref    `json:"ref"`
	ResourceVersion string `json:"resourceVersion"`
	Resource        string `json:"resource"`
	Container       string `json:"container,omitempty"`
	TargetType      string `json:"targetType"`
}
