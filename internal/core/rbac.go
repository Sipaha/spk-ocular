package core

// RBACSnapshot explains observed declarations; it is not an authorization verdict.
type RBACSnapshot struct {
	Principal  string         `json:"principal"`
	Groups     []string       `json:"groups"`
	Subject    *Ref           `json:"subject,omitempty"`
	Accounts   []Ref          `json:"accounts"`
	Grants     []RBACGrant    `json:"grants"`
	Problems   []GraphProblem `json:"problems"`
	Truncated  bool           `json:"truncated"`
	Discovery  string         `json:"discovery"`
	CapturedAt int64          `json:"capturedAt"`
}
type RBACGrant struct {
	Binding     Ref        `json:"binding"`
	BindingKind string     `json:"bindingKind"`
	Role        Ref        `json:"role"`
	RoleKind    string     `json:"roleKind"`
	SubjectKind string     `json:"subjectKind"`
	SubjectName string     `json:"subjectName"`
	Namespace   string     `json:"namespace,omitempty"`
	ClusterWide bool       `json:"clusterWide"`
	Rules       []RBACRule `json:"rules"`
	Error       string     `json:"error,omitempty"`
	Aggregated  bool       `json:"aggregated"`
}
type RBACRule struct {
	Verbs           []string `json:"verbs"`
	APIGroups       []string `json:"apiGroups"`
	Resources       []string `json:"resources"`
	ResourceNames   []string `json:"resourceNames"`
	NonResourceURLs []string `json:"nonResourceURLs"`
}
type AccessAttributes struct {
	Verb        string `json:"verb"`
	Group       string `json:"group"`
	Resource    string `json:"resource"`
	Subresource string `json:"subresource"`
	Namespace   string `json:"namespace"`
	Name        string `json:"name"`
}

// AccessReview uses the current configured connection identity, never impersonation.
type AccessReview struct {
	Attributes      AccessAttributes `json:"attributes"`
	State           string           `json:"state"`
	Reason          string           `json:"reason"`
	EvaluationError string           `json:"evaluationError"`
	CheckedAt       int64            `json:"checkedAt"`
}
