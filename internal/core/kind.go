package core

// ColumnType tells the UI how to render and sort a column.
type ColumnType string

const (
	ColText   ColumnType = "text"
	ColNumber ColumnType = "number"
	ColAge    ColumnType = "age"    // Cell.Time; rendered relative, ticking in the UI
	ColStatus ColumnType = "status" // Cell.Text, coloured by the row's Health
	ColRatio  ColumnType = "ratio"  // Cell.Text "1/3", sorted by Cell.Num (fraction)
	ColCPU    ColumnType = "cpu"    // Cell.Num in cores
	ColBytes  ColumnType = "bytes"  // Cell.Num in bytes
)

type Column struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Type  ColumnType `json:"type"`
	// Width is a hint in px; 0 = flexible.
	Width int `json:"width,omitempty"`
	// ScopeColumn: the scope (namespace) column, hidden when one scope is shown.
	ScopeColumn bool `json:"scopeColumn,omitempty"`
	// Metric: filled from GetMetrics, not from the row itself.
	Metric bool `json:"metric,omitempty"`
}

// Cell is one typed value. The zero Cell is "no value" (null), distinct
// from an empty string or zero: Has* flags say what is set.
type Cell struct {
	Text string `json:"text,omitempty"`
	// Num is the numeric value (number, ratio fraction, cpu, bytes).
	Num *float64 `json:"num,omitempty"`
	// Time is unix milliseconds for age columns.
	Time int64 `json:"time,omitempty"`
	// Muted: shown quieter — evidence (a recent event), not a current state.
	Muted bool `json:"muted,omitempty"`
}

func TextCell(s string) Cell { return Cell{Text: s} }

func NumCell(n float64, text string) Cell { return Cell{Text: text, Num: &n} }

func TimeCell(unixMs int64) Cell { return Cell{Time: unixMs} }

type KindDescriptor struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Singular names one object of the kind ("Deployment"; Title is the
	// plural of the navigation).
	Singular string `json:"singular,omitempty"`
	// Group is the navigation group ("Workloads", "Network", ...).
	Group string `json:"group"`
	// Subgroup, if any, is a collapsible level inside the group (its label).
	Subgroup string   `json:"subgroup,omitempty"`
	Columns  []Column `json:"columns"`
	// Scoped: objects live in a scope (namespace / compose project).
	Scoped bool `json:"scoped"`
	// Hidden kinds are not in the navigation but can be opened (a
	// ReplicaSet reached from a Deployment).
	Hidden bool `json:"hidden,omitempty"`
	// Default marks the kind a first visit of a target opens (none: the
	// first one in the navigation).
	Default bool `json:"default,omitempty"`
	// EventsKind: the kind whose view with Query.Subject = an object of
	// this kind lists the events about it (the details show them); empty:
	// such objects have no events.
	EventsKind string `json:"eventsKind,omitempty"`
	// Logs: objects of this kind have logs (the session is a LogSource).
	Logs bool `json:"logs,omitempty"`
	// Exec: a command can run in objects of this kind (the session is an
	// Execer).
	Exec bool `json:"exec,omitempty"`
	// Forward: ports of objects of this kind can be forwarded (the session
	// is a PortForwarder).
	Forward bool `json:"forward,omitempty"`
	// Actions objects of this kind offer (the session is an Actioner).
	Actions []ActionDescriptor `json:"actions,omitempty"`
	// Editable: objects of this kind can be edited as text (the session is
	// an Editor).
	Editable bool `json:"editable,omitempty"`
	// Aliases are short names of the kind in palette commands (":po"),
	// besides its id and title.
	Aliases []string `json:"aliases,omitempty"`
	// Sort is how a table of this kind is first sorted (nil: by the first
	// column).
	Sort *SortSpec `json:"sort,omitempty"`
	// NotCovered: a view built from several sources (ViewStatus.Coverage
	// lists them) names what it does not look at by design, so "nothing
	// found" is never read as "nothing wrong anywhere".
	NotCovered []string `json:"notCovered,omitempty"`
}

// KindCatalog is what a session offers now (a session whose kinds change
// while it lives — Kubernetes discovery — revises it).
type KindCatalog struct {
	Kinds []KindDescriptor `json:"kinds"`
	// Rev grows with every change within the session.
	Rev uint64 `json:"rev"`
	// State: "ready"; "discovering" (more kinds may come); "partial" (the
	// kinds of Unconfirmed groups are the last known); "failed" (no
	// discovery answered: only the described kinds).
	State string `json:"state"`
	// Unconfirmed are the groups whose kinds could not be confirmed ("*":
	// every named group).
	Unconfirmed []string `json:"unconfirmed,omitempty"`
}

// Catalog states.
const (
	CatalogReady       = "ready"
	CatalogDiscovering = "discovering"
	CatalogPartial     = "partial"
	CatalogFailed      = "failed"
)

// ScopeNames say what the provider's scopes are called (k8s: "Namespace",
// "namespaces", "All namespaces"): the generic UI names no scope itself.
type ScopeNames struct {
	Singular Message `json:"singular"`
	Plural   Message `json:"plural"`
	All      Message `json:"all"`
}

// SortSpec: sort by Column (descending if Desc), ties by Then (ascending).
type SortSpec struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
	Then   string `json:"then,omitempty"`
}

// ScopeMode makes the scope selector explicit instead of overloading "".
type ScopeMode string

const (
	ScopeAll  ScopeMode = "all"  // every scope the user can see
	ScopeOne  ScopeMode = "one"  // ScopeSel.Name
	ScopeNone ScopeMode = "none" // unscoped kinds
)

type ScopeSel struct {
	Mode ScopeMode `json:"mode"`
	Name string    `json:"name,omitempty"`
}

// Valid checks the selector's shape.
func (s ScopeSel) Valid() bool {
	switch s.Mode {
	case ScopeAll, ScopeNone:
		return s.Name == ""
	case ScopeOne:
		return s.Name != ""
	}
	return false
}

type Scope struct {
	Name string `json:"name"`
}
