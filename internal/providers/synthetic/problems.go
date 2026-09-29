package synthetic

import (
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/spk/spk-ocular/internal/provider"
)

// ProblemsKind: a fixed Problems view for UI tests — rows that point at
// other kinds' objects (their menus are those kinds'), evidence shown
// quieter, a source that could not be observed.
const ProblemsKind = "problems"

var problemsKind = core.KindDescriptor{
	ID: ProblemsKind, Title: "Problems", Group: "Health",
	Sort: &core.SortSpec{Column: "severity", Desc: true, Then: "since"},
	Columns: []core.Column{
		{ID: "severity", Title: "Severity", Type: core.ColStatus, Width: 90},
		{ID: "kind", Title: "Kind", Type: core.ColText, Width: 100},
		{ID: "name", Title: "Name", Type: core.ColText},
		{ID: "reason", Title: "Reason", Type: core.ColText, Width: 140},
		{ID: "message", Title: "Message", Type: core.ColText},
		{ID: "since", Title: "Since", Type: core.ColAge, Width: 80},
	},
}

func (s *session) watchProblems(sink provider.Sink) (func(), error) {
	now := time.Now()
	row := func(id, kind, kindTitle, name, severity string, rank float64, state core.HealthState, reason, msg string, ago time.Duration) core.Row {
		sev := core.NumCell(rank, severity)
		sev.Muted = severity == "recent"
		return core.Row{
			ID: id, Rev: "1",
			Ref:    core.Ref{Provider: ID, Target: Target, Kind: kind, Name: name, UID: "uid-" + name},
			Cells:  []core.Cell{sev, core.TextCell(kindTitle), core.TextCell(name), core.TextCell(reason), core.TextCell(msg), core.TimeCell(now.Add(-ago).UnixMilli())},
			Health: core.HealthFrom([]core.Issue{{State: state, Reason: reason, Message: msg, Since: now.Add(-ago).UnixMilli()}}),
		}
	}
	rows := []core.Row{
		row("services#uid-workers", Kind, "Service", "workers", "recent", 1, core.HealthWarning, "BackOff", "back-off restarting (4 in total)", time.Minute),
		row("workloads#uid-db", WorkloadKind, "Workload", "db", "warning", 3, core.HealthWarning, "RecentRestart", "the previous instance ended with exit code 1", 3*time.Minute),
		row("services#uid-api", Kind, "Service", "api", "error", 4, core.HealthError, "CrashLoopBackOff", "container api: back-off", 20*time.Minute),
	}
	sink.Apply(provider.Delta{Reset: true, Upserts: rows, Status: &provider.ViewStatus{State: provider.StatusReady, Coverage: []provider.SourceCoverage{
		{Source: "Services", State: provider.CoverageReady},
		{Source: "Workloads", State: provider.CoverageReady},
		{Source: "Nodes", State: provider.CoverageDenied, Class: provider.ClassForbidden, Message: "nodes is forbidden"},
	}}})
	return func() {}, nil
}
