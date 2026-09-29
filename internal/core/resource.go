package core

import "fmt"

// Ref identifies an object in any provider. Kind is qualified enough to
// avoid collisions ("pods", "apps/deployments"); provider-internal type
// names (GVR) stay inside the adapter. Scope is the namespace / compose
// project, "" for unscoped objects.
type Ref struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	Scope    string `json:"scope,omitempty"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	UID      string `json:"uid,omitempty"`
}

func (r Ref) String() string {
	if r.Scope == "" {
		return fmt.Sprintf("%s/%s", r.Kind, r.Name)
	}
	return fmt.Sprintf("%s/%s/%s", r.Kind, r.Scope, r.Name)
}

// HealthState is ordered by severity for sorting (see Severity).
type HealthState string

const (
	HealthOK          HealthState = "ok"
	HealthProgressing HealthState = "progressing"
	HealthWarning     HealthState = "warning"
	HealthError       HealthState = "error"
	HealthTerminating HealthState = "terminating"
	HealthUnknown     HealthState = "unknown"
)

// Severity: higher is worse; used to pick the summary issue and to sort.
func (s HealthState) Severity() int {
	switch s {
	case HealthError:
		return 5
	case HealthWarning:
		return 4
	case HealthUnknown:
		return 3
	case HealthProgressing:
		return 2
	case HealthTerminating:
		return 1
	}
	return 0
}

// Issue is one detected problem; Health.Issues is ordered worst first.
type Issue struct {
	State   HealthState `json:"state"`
	Reason  string      `json:"reason"`
	Message string      `json:"message,omitempty"`
	// Since is when the issue began, unix milliseconds, if the provider
	// knows it (0: unknown) — not the object's creation or last update.
	Since int64 `json:"since,omitempty"`
}

// Health summarizes an object: State/Reason/Message come from the worst
// issue (or OK with no issues).
type Health struct {
	State   HealthState `json:"state"`
	Reason  string      `json:"reason,omitempty"`
	Message string      `json:"message,omitempty"`
	Issues  []Issue     `json:"issues,omitempty"`
}

// HealthFrom builds the summary from issues (any order); no issues → OK.
func HealthFrom(issues []Issue) Health {
	if len(issues) == 0 {
		return Health{State: HealthOK}
	}
	sorted := append([]Issue(nil), issues...)
	for i := 1; i < len(sorted); i++ { // stable insertion sort, tiny slices
		for j := i; j > 0 && sorted[j].State.Severity() > sorted[j-1].State.Severity(); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	w := sorted[0]
	return Health{State: w.State, Reason: w.Reason, Message: w.Message, Issues: sorted}
}

// Row is the compact, immutable projection of an object for tables. ID is
// unique within its view and changes when the object is replaced (k8s: UID),
// so a same-named replacement is a delete + add. Providers must not mutate a
// Row (or its Cells) after handing it over.
type Row struct {
	ID string `json:"id"`
	// Rev is the provider's revision of the object (k8s resourceVersion):
	// it changes with any change, including fields the table does not show,
	// so an open details panel knows to refresh.
	Rev    string `json:"rev,omitempty"`
	Ref    Ref    `json:"ref"`
	Cells  []Cell `json:"cells"`
	Health Health `json:"health"`
}

// Relation links an object to another one.
type Relation struct {
	// Type: "owner" (up), "owns" (down), "selects", "routes-to", "runs-on",
	// "about" (what an event is about).
	Type string `json:"type"`
	Ref  Ref    `json:"ref"`
	// Inert: named, not openable — a kind the provider does not show, or an
	// object it cannot pin down (opening by name could show another one).
	Inert bool `json:"inert,omitempty"`
}

// Resource is the full view of one object for the details panel.
type Resource struct {
	Ref       Ref        `json:"ref"`
	Health    Health     `json:"health"`
	Facts     []Detail   `json:"facts"`
	YAML      string     `json:"yaml"`
	Relations []Relation `json:"relations"`
	// RelationsError: some relations could not be looked up (denied, slow);
	// the rest of the resource is still valid.
	RelationsError string `json:"relationsError,omitempty"`
	// RelationsTruncated: a relation list hit its cap (200); more exist.
	RelationsTruncated bool `json:"relationsTruncated,omitempty"`
}
