package core

// GatekeeperAudit is one constraint's saved producer evidence, not a new audit.
type GatekeeperAudit struct {
	Ref                Ref                   `json:"ref"`
	APIVersion         string                `json:"apiVersion"`
	ResourceVersion    string                `json:"resourceVersion"`
	CapturedAt         int64                 `json:"capturedAt"`
	AuditTime          string                `json:"auditTime,omitempty"`
	AuditTimeState     string                `json:"auditTimeState"`
	TotalViolations    *int64                `json:"totalViolations,omitempty"`
	ReturnedViolations int                   `json:"returnedViolations"`
	Coverage           string                `json:"coverage"`
	Partial            bool                  `json:"partial"`
	Violations         []GatekeeperViolation `json:"violations"`
}

// Subjects have no verified UID; none is a navigation Ref.
type GatekeeperViolation struct {
	Group              string   `json:"group"`
	Version            string   `json:"version"`
	Kind               string   `json:"kind"`
	Namespace          string   `json:"namespace"`
	Name               string   `json:"name"`
	Message            string   `json:"message"`
	EnforcementAction  string   `json:"enforcementAction"`
	EnforcementActions []string `json:"enforcementActions"`
}
