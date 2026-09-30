package compose

import "github.com/spk/spk-ocular/internal/core"

// Navigation groups.
const (
	groupCompose = "Compose"
	groupEngine  = "Engine"
)

// projectColumn is the scope column of rows that belong to a project.
var projectColumn = core.Column{ID: "project", Title: "Project", Type: core.ColText, ScopeColumn: true}

// kindDescriptors are the provider's kinds in navigation order (decision
// 5). Palette aliases are unique within the provider and none is a scope
// or target command ("project", "proj", "ctx", "context" — provider.go):
// ":project web" selects a project, so the projects kind answers only to
// its id and title. No kind has events (no events view in P6).
func kindDescriptors() []core.KindDescriptor {
	return []core.KindDescriptor{
		{
			ID: KindProjects, Title: "Projects", Singular: "Project", Group: groupCompose,
			Columns: []core.Column{
				{ID: "name", Title: "Name", Type: core.ColText},
				{ID: "services", Title: "Services", Type: core.ColNumber, Width: 80},
				{ID: "containers", Title: "Containers", Type: core.ColNumber, Width: 90},
				{ID: "running", Title: "Running", Type: core.ColNumber, Width: 80},
				{ID: "workingDir", Title: "Working dir", Type: core.ColText},
				{ID: "configFiles", Title: "Config files", Type: core.ColText},
			},
		},
		{
			ID: KindServices, Title: "Services", Singular: "Service", Group: groupCompose,
			Scoped: true, Default: true, Logs: true, Exec: true, Aliases: []string{"svc", "service"},
			Columns: []core.Column{
				{ID: "name", Title: "Name", Type: core.ColText},
				projectColumn,
				{ID: "running", Title: "Running", Type: core.ColRatio, Width: 80},
				{ID: "status", Title: "Status", Type: core.ColStatus, Width: 150},
				{ID: "images", Title: "Images", Type: core.ColText},
				{ID: "cpu", Title: "CPU", Type: core.ColCPU, Width: 70, Metric: true},
				{ID: "memory", Title: "Memory", Type: core.ColBytes, Width: 80, Metric: true},
			},
		},
		{
			ID: KindContainers, Title: "Containers", Singular: "Container", Group: groupCompose,
			Scoped: true, Logs: true, Exec: true, Aliases: []string{"ct", "container"},
			NotCovered: []string{msg("notCovered.unlabelled").Text},
			Columns: []core.Column{
				{ID: "name", Title: "Name", Type: core.ColText},
				projectColumn,
				{ID: "service", Title: "Service", Type: core.ColText},
				{ID: "number", Title: "#", Type: core.ColNumber, Width: 50},
				{ID: "status", Title: "Status", Type: core.ColStatus, Width: 130},
				{ID: "health", Title: "Health", Type: core.ColText, Width: 90},
				{ID: "restarts", Title: "Restarts", Type: core.ColNumber, Width: 80},
				{ID: "image", Title: "Image", Type: core.ColText},
				{ID: "ports", Title: "Ports", Type: core.ColText},
				{ID: "age", Title: "Age", Type: core.ColAge, Width: 70},
				{ID: "cpu", Title: "CPU", Type: core.ColCPU, Width: 70, Metric: true},
				{ID: "memory", Title: "Memory", Type: core.ColBytes, Width: 80, Metric: true},
			},
		},
		{
			ID: KindNetworks, Title: "Networks", Singular: "Network", Group: groupEngine,
			Scoped: true, Aliases: []string{"net", "network"},
			Columns: []core.Column{
				{ID: "name", Title: "Name", Type: core.ColText},
				projectColumn,
				{ID: "driver", Title: "Driver", Type: core.ColText, Width: 90},
				{ID: "netScope", Title: "Scope", Type: core.ColText, Width: 70},
				{ID: "containers", Title: "Containers", Type: core.ColNumber, Width: 90},
				{ID: "age", Title: "Age", Type: core.ColAge, Width: 70},
			},
		},
		{
			ID: KindVolumes, Title: "Volumes", Singular: "Volume", Group: groupEngine,
			Scoped: true, Aliases: []string{"vol", "volume"},
			Columns: []core.Column{
				{ID: "name", Title: "Name", Type: core.ColText},
				projectColumn,
				{ID: "driver", Title: "Driver", Type: core.ColText, Width: 90},
				{ID: "usedBy", Title: "Used by", Type: core.ColNumber, Width: 80},
				{ID: "age", Title: "Age", Type: core.ColAge, Width: 70},
			},
		},
		{
			ID: KindImages, Title: "Images", Singular: "Image", Group: groupEngine,
			Aliases: []string{"img", "image"},
			Columns: []core.Column{
				{ID: "tags", Title: "Tags", Type: core.ColText},
				{ID: "id", Title: "Id", Type: core.ColText, Width: 120},
				{ID: "size", Title: "Size", Type: core.ColBytes, Width: 90},
				{ID: "usedBy", Title: "Used by", Type: core.ColNumber, Width: 80},
				{ID: "age", Title: "Age", Type: core.ColAge, Width: 70},
			},
		},
	}
}
