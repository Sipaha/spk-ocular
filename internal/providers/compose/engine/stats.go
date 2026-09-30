package engine

import (
	"context"
	"net/url"
	"time"
)

// DefaultStatsBytes bounds a stats answer.
const DefaultStatsBytes = 256 << 10

// Stats is one sample of a container's usage (GET /containers/{id}/stats
// with stream=false). A stopped container's has a zero Read.
type Stats struct {
	Read        time.Time   `json:"read"`
	OSType      string      `json:"os_type"`
	CPUStats    CPUStats    `json:"cpu_stats"`
	PreCPUStats CPUStats    `json:"precpu_stats"` // zero in a one-shot sample
	MemoryStats MemoryStats `json:"memory_stats"`
}

type CPUStats struct {
	CPUUsage struct {
		TotalUsage  int64   `json:"total_usage"`            // ns of CPU time
		PercpuUsage []int64 `json:"percpu_usage,omitempty"` // cgroup v1 only
	} `json:"cpu_usage"`
	SystemCPUUsage int64 `json:"system_cpu_usage"` // ns, all CPUs
	OnlineCPUs     int   `json:"online_cpus"`
}

// MemoryStats: Stats holds the cgroup's counters (v2: inactive_file; v1:
// total_inactive_file, cache).
type MemoryStats struct {
	Usage int64            `json:"usage"`
	Limit int64            `json:"limit"`
	Stats map[string]int64 `json:"stats"`
}

// ContainerStats reads one sample. oneShot answers at once without the
// previous sample; otherwise the daemon waits ~1 s and fills PreCPUStats.
func (c *Client) ContainerStats(ctx context.Context, id string, oneShot bool) (Stats, error) {
	q := url.Values{"stream": {"false"}}
	if oneShot {
		q.Set("one-shot", "true")
	}
	var v Stats
	err := c.getJSON(ctx, "/containers/"+url.PathEscape(id)+"/stats", q, DefaultStatsBytes, &v)
	return v, err
}
