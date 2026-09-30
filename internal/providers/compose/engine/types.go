package engine

import (
	"encoding/json"
	"time"
)

// The decoded types keep only what Ocular's views, details and health
// read; every inspect also keeps its whole answer in Raw (the details show
// it). Field names follow the Engine API; unknown fields are ignored.

// Time is an Engine timestamp (RFC 3339 text). "" and unparsable text
// decode to the zero time without failing the object; Raw keeps the text
// as sent (volume identity uses it verbatim).
type Time struct {
	time.Time
	Raw string
}

func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil {
		*t = Time{}
		return nil
	}
	t.Raw = s
	t.Time, _ = time.Parse(time.RFC3339Nano, s)
	if t.Year() <= 1 { // "0001-01-01T00:00:00Z": never
		t.Time = time.Time{}
	}
	return nil
}

func (t Time) MarshalJSON() ([]byte, error) {
	if t.Raw == "" && !t.IsZero() {
		return json.Marshal(t.Format(time.RFC3339Nano))
	}
	return json.Marshal(t.Raw)
}

// TimeOf makes a Time as the Engine would send it.
func TimeOf(t time.Time) Time { return Time{Time: t, Raw: t.Format(time.RFC3339Nano)} }

// Ping is the answer of GET /_ping.
type Ping struct {
	APIVersion string // what the daemon reported
	OSType     string
	// Version is what the client speaks with it (min(MaxAPIVersion, daemon)).
	Version string
}

type Info struct {
	ID              string `json:"ID"`
	Name            string `json:"Name"`
	ServerVersion   string `json:"ServerVersion"`
	OSType          string `json:"OSType"`
	OperatingSystem string `json:"OperatingSystem"`
	Architecture    string `json:"Architecture"`
	KernelVersion   string `json:"KernelVersion"`
	// SystemTime is the daemon's clock (observation starts events from it,
	// not from the local clock).
	SystemTime Time `json:"SystemTime"`
}

// Filters of a list or events request: key → values (label: "k" or "k=v").
type Filters map[string][]string

type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Created int64             `json:"Created"`
	State   string            `json:"State"`
	Status  string            `json:"Status"`
	Labels  map[string]string `json:"Labels"`
}

type ContainerInspect struct {
	ID              string          `json:"Id"`
	Name            string          `json:"Name"` // with the leading "/"
	Created         Time            `json:"Created"`
	Image           string          `json:"Image"` // the image id
	RestartCount    int             `json:"RestartCount"`
	State           ContainerState  `json:"State"`
	Config          ContainerConfig `json:"Config"`
	HostConfig      HostConfig      `json:"HostConfig"`
	NetworkSettings NetworkSettings `json:"NetworkSettings"`
	Mounts          []Mount         `json:"Mounts"`
	// Raw is the whole inspect answer as sent.
	Raw []byte `json:"-"`
}

type ContainerState struct {
	Status     string  `json:"Status"` // created running paused restarting removing exited dead
	Running    bool    `json:"Running"`
	Paused     bool    `json:"Paused"`
	Restarting bool    `json:"Restarting"`
	OOMKilled  bool    `json:"OOMKilled"`
	Dead       bool    `json:"Dead"`
	ExitCode   int     `json:"ExitCode"`
	Error      string  `json:"Error"`
	StartedAt  Time    `json:"StartedAt"`
	FinishedAt Time    `json:"FinishedAt"`
	Health     *Health `json:"Health,omitempty"` // nil: no healthcheck
}

type Health struct {
	Status        string         `json:"Status"` // starting healthy unhealthy
	FailingStreak int            `json:"FailingStreak"`
	Log           []HealthResult `json:"Log"`
}

type HealthResult struct {
	Start    Time   `json:"Start"`
	End      Time   `json:"End"`
	ExitCode int    `json:"ExitCode"`
	Output   string `json:"Output"`
}

// ContainerConfig: no Env (it may hold secrets; Raw has it for details).
type ContainerConfig struct {
	Hostname string            `json:"Hostname"`
	Image    string            `json:"Image"` // as asked (a reference)
	Labels   map[string]string `json:"Labels"`
	Tty      bool              `json:"Tty"` // logs: one raw stream, no stdcopy
}

type HostConfig struct {
	NetworkMode   string                   `json:"NetworkMode"`
	PortBindings  map[string][]PortBinding `json:"PortBindings"`
	RestartPolicy RestartPolicy            `json:"RestartPolicy"`
	LogConfig     LogConfig                `json:"LogConfig"`
}

type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name              string `json:"Name"`
	MaximumRetryCount int    `json:"MaximumRetryCount"`
}

type LogConfig struct {
	Type string `json:"Type"` // the log driver (none: no logs)
}

type NetworkSettings struct {
	// Ports: "80/tcp" → published bindings (nil: exposed, not published).
	Ports    map[string][]PortBinding    `json:"Ports"`
	Networks map[string]EndpointSettings `json:"Networks"` // by network name
}

type EndpointSettings struct {
	NetworkID         string   `json:"NetworkID"`
	EndpointID        string   `json:"EndpointID"`
	Gateway           string   `json:"Gateway"`
	IPAddress         string   `json:"IPAddress"`
	IPPrefixLen       int      `json:"IPPrefixLen"`
	GlobalIPv6Address string   `json:"GlobalIPv6Address"`
	MacAddress        string   `json:"MacAddress"`
	Aliases           []string `json:"Aliases"`
	DNSNames          []string `json:"DNSNames"`
}

type Mount struct {
	Type        string `json:"Type"` // bind volume tmpfs npipe cluster image
	Name        string `json:"Name"` // the volume's name
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	Driver      string `json:"Driver"`
	Mode        string `json:"Mode"`
	RW          bool   `json:"RW"`
}

// Network is a list item and an inspect answer (Containers only in inspect).
type Network struct {
	ID         string                     `json:"Id"`
	Name       string                     `json:"Name"`
	Created    Time                       `json:"Created"`
	Scope      string                     `json:"Scope"`
	Driver     string                     `json:"Driver"`
	Internal   bool                       `json:"Internal"`
	Attachable bool                       `json:"Attachable"`
	Labels     map[string]string          `json:"Labels"`
	Containers map[string]NetworkEndpoint `json:"Containers"` // by container id
	Raw        []byte                     `json:"-"`          // inspect only
}

type NetworkEndpoint struct {
	Name        string `json:"Name"`
	EndpointID  string `json:"EndpointID"`
	MacAddress  string `json:"MacAddress"`
	IPv4Address string `json:"IPv4Address"`
	IPv6Address string `json:"IPv6Address"`
}

// Volume is a list item and an inspect answer. CreatedAt stays text: it
// is part of a volume's best-effort identity as sent.
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt,omitempty"`
	Labels     map[string]string `json:"Labels"`
	Scope      string            `json:"Scope"`
	Raw        []byte            `json:"-"` // inspect only
}

// VolumeList: Warnings non-empty means the list may be incomplete (a
// volume driver did not answer).
type VolumeList struct {
	Volumes  []Volume `json:"Volumes"`
	Warnings []string `json:"Warnings"`
}

type ImageSummary struct {
	ID          string            `json:"Id"`
	ParentID    string            `json:"ParentId"`
	RepoTags    []string          `json:"RepoTags"`
	RepoDigests []string          `json:"RepoDigests"`
	Created     int64             `json:"Created"` // unix seconds
	Size        int64             `json:"Size"`
	Labels      map[string]string `json:"Labels"`
	Containers  int64             `json:"Containers"`
}

type ImageInspect struct {
	ID           string      `json:"Id"`
	RepoTags     []string    `json:"RepoTags"`
	RepoDigests  []string    `json:"RepoDigests"`
	Created      Time        `json:"Created"` // may be absent
	Size         int64       `json:"Size"`
	Os           string      `json:"Os"`
	Architecture string      `json:"Architecture"`
	Config       ImageConfig `json:"Config"`
	Raw          []byte      `json:"-"`
}

type ImageConfig struct {
	Labels map[string]string `json:"Labels"`
}

// Event is one message of the events stream.
type Event struct {
	Type     string     `json:"Type"` // container network volume image …
	Action   string     `json:"Action"`
	Actor    EventActor `json:"Actor"`
	Scope    string     `json:"scope"`
	Time     int64      `json:"time"`     // unix seconds
	TimeNano int64      `json:"timeNano"` // unix nanoseconds
}

type EventActor struct {
	ID         string            `json:"ID"`
	Attributes map[string]string `json:"Attributes"`
}

// When is the event's time (nanoseconds when sent).
func (e Event) When() time.Time {
	if e.TimeNano != 0 {
		return time.Unix(0, e.TimeNano)
	}
	return time.Unix(e.Time, 0)
}
