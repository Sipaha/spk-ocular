package core

// SecurityOptions controls explicit additional report acquisition. Namespace
// selection alone never opts into cluster-scoped audit reports.
type SecurityOptions struct {
	IncludeClusterReports       bool
	IncludeClusterPolicyReports bool
	PolicyReportAPI             string
}

func (o SecurityOptions) Valid() bool {
	return (o.PolicyReportAPI == "" || o.PolicyReportAPI == "wgpolicy" || o.PolicyReportAPI == "openreports") && (!o.IncludeClusterPolicyReports || o.PolicyReportAPI != "")
}

// SecuritySnapshot contains declarations and scanner reports, not exploitability
// or a cluster certification. Raw workload values and Secret inventories are
// excluded; admitted scanner text is untrusted producer data.
type SecuritySnapshot struct {
	Findings     []SecurityFinding     `json:"findings"`
	Problems     []GraphProblem        `json:"problems"`
	Discovery    string                `json:"discovery"`
	Truncated    bool                  `json:"truncated"`
	CapturedAt   int64                 `json:"capturedAt"`
	Resources    int                   `json:"resources"`
	Reports      int                   `json:"reports"`
	CVEState     string                `json:"cveState"`
	AuditSources []SecurityAuditSource `json:"auditSources"`
}

// SecurityAuditSource describes acquisition, never a compliance verdict.
type SecurityAuditSource struct {
	Kind          string `json:"kind"`
	ClusterScoped bool   `json:"clusterScoped"`
	APIVersion    string `json:"apiVersion"`
	State         string `json:"state"`
	Reports       int    `json:"reports"`
}

// SecurityAuditCheck retains source-native evidence. Owner identity is descriptive
// only; the report Ref remains the sole navigation target. Freshness is unknown.
type SecurityAuditCheck struct {
	Result          string   `json:"result"`
	NativeSeverity  string   `json:"nativeSeverity"`
	Category        string   `json:"category"`
	Description     string   `json:"description"`
	Messages        []string `json:"messages"`
	Remediation     string   `json:"remediation"`
	ScopeType       string   `json:"scopeType"`
	ScopeValue      string   `json:"scopeValue"`
	ScannerName     string   `json:"scannerName"`
	ScannerVersion  string   `json:"scannerVersion"`
	ScannerVendor   string   `json:"scannerVendor"`
	APIVersion      string   `json:"apiVersion"`
	ResourceVersion string   `json:"resourceVersion"`
	OwnerUID        string   `json:"ownerUid"`
}

// Policy subjects describe producer evidence only. They never authorize reads
// or navigation; the finding Ref identifies the source report itself.
type SecurityPolicySubject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type SecurityPolicyCheck struct {
	Result          string                  `json:"result"`
	NativeResult    string                  `json:"nativeResult"`
	NativeSeverity  string                  `json:"nativeSeverity"`
	Source          string                  `json:"source"`
	Policy          string                  `json:"policy"`
	Rule            string                  `json:"rule"`
	Category        string                  `json:"category"`
	Scored          *bool                   `json:"scored"`
	Message         string                  `json:"message"`
	Process         string                  `json:"process"`
	Exceptions      string                  `json:"exceptions"`
	APIVersion      string                  `json:"apiVersion"`
	ResourceVersion string                  `json:"resourceVersion"`
	Scope           *SecurityPolicySubject  `json:"scope"`
	Subjects        []SecurityPolicySubject `json:"subjects"`
	ScopeSelector   string                  `json:"scopeSelector"`
	SubjectSelector string                  `json:"subjectSelector"`
	MaxResults      *int64                  `json:"maxResults"`
	StatusFilter    []string                `json:"statusFilter"`
	Summary         map[string]int64        `json:"summary"`
}
type SecurityFinding struct {
	ID        string `json:"id"`
	Ref       Ref    `json:"ref"`
	KindTitle string `json:"kindTitle"`
	Category  string `json:"category"`
	Severity  string `json:"severity"`
	Rule      string `json:"rule"`
	Container string `json:"container,omitempty"`
	Evidence  string `json:"evidence"`
	// Scanner text is external data. The report Ref is the navigation source
	// when its kind is available in the current catalogue;
	// owner metadata is descriptive only and does not establish image freshness.
	Title     string               `json:"title,omitempty"`
	Image     string               `json:"image,omitempty"`
	Package   string               `json:"package,omitempty"`
	Installed string               `json:"installed,omitempty"`
	Fixed     string               `json:"fixed,omitempty"`
	Scanner   string               `json:"scanner,omitempty"`
	ScannedAt int64                `json:"scannedAt,omitempty"`
	Workload  string               `json:"workload,omitempty"`
	Audit     *SecurityAuditCheck  `json:"audit,omitempty"`
	Policy    *SecurityPolicyCheck `json:"policy,omitempty"`
}
