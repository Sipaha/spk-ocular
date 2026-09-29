package desktop

import "strings"

// gpuPolicy names the WebKitGTK hardware-acceleration policy.
type gpuPolicy int

const (
	gpuDefault gpuPolicy = iota
	gpuAlways
	gpuOnDemand
	gpuNever
)

// parseGPUPolicy reads SPK_OCULAR_GPU (always|ondemand|never); anything else
// keeps the default. An escape hatch for broken GPU drivers.
func parseGPUPolicy(v string) gpuPolicy {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "always":
		return gpuAlways
	case "ondemand", "on-demand":
		return gpuOnDemand
	case "never", "off":
		return gpuNever
	}
	return gpuDefault
}
