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
}

func TextCell(s string) Cell { return Cell{Text: s} }

func NumCell(n float64, text string) Cell { return Cell{Text: text, Num: &n} }

func TimeCell(unixMs int64) Cell { return Cell{Time: unixMs} }

type KindDescriptor struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Group is the navigation group ("Workloads", "Network", ...).
	Group   string   `json:"group"`
	Columns []Column `json:"columns"`
	// Scoped: objects live in a scope (namespace / compose project).
	Scoped bool `json:"scoped"`
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
