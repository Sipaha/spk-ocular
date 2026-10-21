package core

// NetworkPath is a bounded observation of declared wiring, never a probe or a
// reachability verdict. Each route and endpoint retains its own evidence.
type NetworkPath struct {
	Ref        Ref               `json:"ref"`
	CapturedAt int64             `json:"capturedAt"`
	Discovery  string            `json:"discovery"`
	Routes     []NetworkRoute    `json:"routes"`
	Coverage   []NetworkCoverage `json:"coverage"`
	Truncated  bool              `json:"truncated"`
}
type NetworkCoverage struct {
	Source string `json:"source"`
	State  string `json:"state"`
}
type NetworkCheck struct {
	Code  string      `json:"code"`
	State HealthState `json:"state"`
	Field string      `json:"field"`
	Ref   *Ref        `json:"ref,omitempty"`
}
type NetworkRoute struct {
	Route        *IngressRoute     `json:"route,omitempty"`
	Field        string            `json:"field"`
	Service      Ref               `json:"service"`
	Mode         string            `json:"mode"`
	ExternalName string            `json:"externalName,omitempty"`
	Selector     string            `json:"selector,omitempty"`
	PortName     string            `json:"portName,omitempty"`
	Port         int64             `json:"port"`
	TargetPort   string            `json:"targetPort"`
	Protocol     string            `json:"protocol"`
	Checks       []NetworkCheck    `json:"checks"`
	Endpoints    []NetworkEndpoint `json:"endpoints"`
}
type NetworkEndpoint struct {
	Slice       Ref            `json:"slice"`
	Pod         *Ref           `json:"pod,omitempty"`
	Owners      []Ref          `json:"owners"`
	Addresses   []string       `json:"addresses"`
	Port        int64          `json:"port"`
	Ready       *bool          `json:"ready"`
	Serving     *bool          `json:"serving"`
	Terminating *bool          `json:"terminating"`
	Checks      []NetworkCheck `json:"checks"`
}
