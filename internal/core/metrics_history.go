package core

// HistorySource is explicit UI input, never persisted or returned as metadata.
// Token must not enter errors, logs, preferences, events or agent responses.
type HistorySource struct {
	URL          string `json:"url"`
	Token        string `json:"token,omitempty"`
	Dedicated    bool   `json:"dedicated"`
	ClusterLabel string `json:"clusterLabel,omitempty"`
	ClusterValue string `json:"clusterValue,omitempty"`
}
type HistoryRequest struct {
	Ref    Ref           `json:"ref"`
	Source HistorySource `json:"source"`
	Window string        `json:"window"`
}
type HistorySeries struct {
	Container   string     `json:"container"`
	Incarnation string     `json:"incarnation"`
	Values      []*float64 `json:"values"`
}
type HistoryMetric struct {
	Name              string          `json:"name"`
	MissingContainers []string        `json:"missingContainers"`
	State             string          `json:"state"`
	Series            []HistorySeries `json:"series"`
}
type HistoryDeclaration struct {
	Container     string   `json:"container"`
	CPURequest    *float64 `json:"cpuRequest"`
	CPULimit      *float64 `json:"cpuLimit"`
	MemoryRequest *float64 `json:"memoryRequest"`
	MemoryLimit   *float64 `json:"memoryLimit"`
}
type MetricsHistory struct {
	Ref             Ref                  `json:"ref"`
	ResourceVersion string               `json:"resourceVersion"`
	Source          string               `json:"source"`
	Scope           string               `json:"scope"`
	CapturedAt      int64                `json:"capturedAt"`
	RequestedStart  int64                `json:"requestedStart"`
	Start           int64                `json:"start"`
	End             int64                `json:"end"`
	Step            int64                `json:"step"`
	Metrics         []HistoryMetric      `json:"metrics"`
	Declarations    []HistoryDeclaration `json:"declarations"`
}
