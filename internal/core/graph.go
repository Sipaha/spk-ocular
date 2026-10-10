package core

// Graph is a bounded, value-free inventory snapshot. Edges represent declared
// relationships, never observed traffic. Ref + UID pins the details selection.
type Graph struct {
	Nodes      []GraphNode    `json:"nodes"`
	Edges      []GraphEdge    `json:"edges"`
	Problems   []GraphProblem `json:"problems"`
	Truncated  bool           `json:"truncated"`
	Discovery  string         `json:"discovery"`
	CapturedAt int64          `json:"capturedAt"`
}
type GraphNode struct {
	ID        string      `json:"id"`
	Ref       Ref         `json:"ref"`
	KindTitle string      `json:"kindTitle"`
	Health    HealthState `json:"health"`
}
type GraphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}
type GraphProblem struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope,omitempty"`
	Class string `json:"class"`
}
