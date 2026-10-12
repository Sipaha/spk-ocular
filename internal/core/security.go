package core

// SecuritySnapshot contains declarations and scanner reports, not exploitability
// or a cluster certification. Findings never contain workload values or Secrets.
type SecuritySnapshot struct {
	Findings   []SecurityFinding `json:"findings"`
	Problems   []GraphProblem    `json:"problems"`
	Discovery  string            `json:"discovery"`
	Truncated  bool              `json:"truncated"`
	CapturedAt int64             `json:"capturedAt"`
	Resources  int               `json:"resources"`
	Reports    int               `json:"reports"`
	CVEState   string            `json:"cveState"`
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
	Title     string `json:"title,omitempty"`
	Image     string `json:"image,omitempty"`
	Package   string `json:"package,omitempty"`
	Installed string `json:"installed,omitempty"`
	Fixed     string `json:"fixed,omitempty"`
	Scanner   string `json:"scanner,omitempty"`
	ScannedAt int64  `json:"scannedAt,omitempty"`
	Workload  string `json:"workload,omitempty"`
}
