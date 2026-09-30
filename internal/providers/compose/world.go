package compose

import (
	"github.com/spk/spk-ocular/internal/providers/compose/engine"
)

// The Compose labels (docker compose v2/v5 set them on containers, networks
// and volumes it creates).
const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelNumber      = "com.docker.compose.container-number"
	LabelOneoff      = "com.docker.compose.oneoff"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
	LabelNetwork     = "com.docker.compose.network"
	LabelVolume      = "com.docker.compose.volume"
)

// Kinds of the provider.
const (
	KindProjects   = "projects"
	KindServices   = "services"
	KindContainers = "containers"
	KindNetworks   = "networks"
	KindVolumes    = "volumes"
	KindImages     = "images"
)

// Feed is one type of Engine object a session observes (one list + events
// + inspect loop per type, leased by the views that need it).
type Feed int

const (
	FeedContainers Feed = iota
	FeedNetworks
	FeedVolumes
	FeedImages
	feedCount
)

func (f Feed) String() string {
	return [...]string{"containers", "networks", "volumes", "images"}[f]
}

// World is one consistent reading of a session's feeds for projections:
// the inspected objects of the feeds a view leases (Has says which; a
// feed not leased is absent, not empty). Projections never mutate it.
type World struct {
	Containers map[string]*engine.ContainerInspect // by full id
	Networks   map[string]*engine.Network          // by id (inspect answers)
	Volumes    map[string]*engine.Volume           // by name (inspect answers)
	Images     map[string]*engine.ImageInspect     // by id ("sha256:…")
	Has        [feedCount]bool
}
