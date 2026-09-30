package provider

import (
	"context"
	"time"

	"github.com/spk/spk-ocular/internal/core"
)

// Opener is implemented by providers whose targets can be connected to.
type Opener interface {
	// Open builds a session for a target from its current configuration. It
	// must not block on the network: connection problems surface as view
	// statuses. ConfigHash identifies the configuration the session was built
	// from; when Discover later reports a different hash, the session is stale.
	Open(ctx context.Context, target string) (Session, error)
}

// Session is a connection to one target.
type Session interface {
	// ConfigHash of the target configuration this session was built from.
	ConfigHash() string
	Kinds() []core.KindDescriptor
	// Scopes lists scopes (namespaces / compose projects). A permission error
	// is returned as *Error{Class: ClassForbidden}: the UI then lets the
	// user type a scope instead of pretending there are none.
	Scopes(ctx context.Context) ([]core.Scope, error)
	// ScopeKind is the kind whose rows are the scopes ("namespaces"), so the
	// UI can keep the scope list live; "" if scopes cannot be watched.
	ScopeKind() string
	// Watch starts feeding sink with q's rows until stop is called. The first
	// deliveries establish the initial state and end with Status{Ready} once
	// it is fully processed (not merely listed); later deliveries are changes.
	Watch(q Query, sink Sink) (stop func(), err error)
	// Get returns the full object; ref.UID, when set, must match (a
	// same-named replacement is ErrGone, not silently another object).
	Get(ctx context.Context, ref core.Ref) (*core.Resource, error)
	Close()
}

// Resyncer is implemented by sessions whose observation is best effort
// (Docker: events can be lost without a break): the user can ask to read a
// view's sources again. Resync starts a new observation of what q depends
// on and returns at once; concurrent requests coalesce, rows stay until
// the new reading succeeds (a failed one leaves them, stale). Not polling:
// only on the user's request.
type Resyncer interface {
	Resync(q Query) error
}

// Cataloger is implemented by sessions whose kinds change while they live
// (Kubernetes: discovery of served resources).
type Cataloger interface {
	// Catalog: the kinds now with their revision (one snapshot; Kinds()
	// is its Kinds).
	Catalog() core.KindCatalog
	// OnKindsChanged sets what is told of each new revision (set once, by
	// the API, before anyone reads the catalog). Called outside the
	// session's locks; must not block.
	OnKindsChanged(func(rev uint64))
	// RefreshKinds reads the kinds again in the background (F5); a new
	// revision, if any, is told.
	RefreshKinds()
	// KindRemoved: the kind was offered by this session and is not served
	// any more (a view of it ends ClassRemoved, and is not reopened).
	KindRemoved(id string) bool
}

// ViewDescriber is implemented by sessions whose kinds' columns are known
// per view (server-side tables): OpenView asks for the view's descriptor —
// which may read the server, bounded by ctx — and opens the query it
// returns (bound to those columns: Query.Schema).
type ViewDescriber interface {
	DescribeView(ctx context.Context, q Query) (core.KindDescriptor, Query, error)
}

// Identifier is implemented by sessions whose target's identity needs the
// target itself (Docker: the daemon's id). Identity may ask it (bounded by
// ctx) once per session; an unreachable target is an error, never another
// identity. It completes core.Target.Identity, it does not replace it.
type Identifier interface {
	Identity(ctx context.Context) (string, error)
}

// Query is what a view shows. It is immutable for the view's lifetime.
type Query struct {
	Kind  string        `json:"kind"`
	Scope core.ScopeSel `json:"scope"`
	// Subject narrows the kind to objects about one object (events of a pod).
	// Providers translate it (k8s: involvedObject.uid); nil = no narrowing.
	Subject *core.Ref `json:"subject,omitempty"`
	// Name narrows to one object by name (an open details panel follows it).
	Name string `json:"name,omitempty"`
	// Schema binds the view to the columns its descriptor has (set by the
	// API from ViewDescriber; 0: the kind's fixed columns).
	Schema uint64 `json:"schema,omitempty"`
}

// Sink receives a watch's deliveries. Apply is called synchronously from the
// provider's event handlers, in order; it must not block (it only updates
// in-memory state) and takes ownership of d's slices.
type Sink interface {
	Apply(d Delta)
}

// Delta is one delivery. Applied in this order: Reset (replace all rows by
// Upserts atomically) or Upserts, then Deletes, then Status.
type Delta struct {
	Reset   bool       `json:"reset,omitempty"`
	Upserts []core.Row `json:"upserts,omitempty"`
	// Deletes are row IDs. Deleting an ID that is not present is a no-op (a
	// late delete of a replaced object's old UID must not remove the new row).
	Deletes []string    `json:"deletes,omitempty"`
	Status  *ViewStatus `json:"status,omitempty"`
}

type StatusState string

const (
	StatusLoading StatusState = "loading" // no initial state yet
	StatusReady   StatusState = "ready"   // initial state processed; rows are live
	StatusStale   StatusState = "stale"   // lost the connection; rows are the last known
	StatusError   StatusState = "error"   // cannot show rows (see Class)
)

// ErrorClass classifies failures for the UI; stable strings.
type ErrorClass string

const (
	ClassForbidden    ErrorClass = "forbidden"
	ClassUnauthorized ErrorClass = "unauthorized"
	ClassUnavailable  ErrorClass = "unavailable"
	ClassGone         ErrorClass = "gone"
	// ClassRemoved: the kind is no longer served (a CRD deleted); unlike
	// gone, reopening the view cannot help.
	ClassRemoved ErrorClass = "removed"
	// ClassSchemaChanged: the kind's columns changed; the view ended — open
	// it again (it gets the new columns).
	ClassSchemaChanged ErrorClass = "schema_changed"
	ClassNotFound      ErrorClass = "not_found"
	ClassUnsupported   ErrorClass = "unsupported"
	ClassConflict      ErrorClass = "conflict"
	// ClassUnknown: a change was sent but its outcome is not known (the
	// connection ended before the answer); never retried automatically.
	ClassUnknown ErrorClass = "unknown"
	// ClassInvalid: the request cannot be done as asked (a container that
	// is not running, a command that does not exist).
	ClassInvalid  ErrorClass = "bad_request"
	ClassInternal ErrorClass = "internal"
)

type ViewStatus struct {
	State   StatusState `json:"state"`
	Class   ErrorClass  `json:"class,omitempty"`
	Message string      `json:"message,omitempty"`
	// Coverage: a view built from several sources says how each one is
	// (in the provider's fixed order), so an empty result with a source
	// not seen is not "all is well". Compare with Equal, copy with Clone.
	Coverage []SourceCoverage `json:"coverage,omitempty"`
}

// SourceCoverage is how one source of a view is observed.
type SourceCoverage struct {
	Source  string        `json:"source"`
	State   CoverageState `json:"state"`
	Class   ErrorClass    `json:"class,omitempty"`
	Message string        `json:"message,omitempty"`
}

type CoverageState string

const (
	CoverageLoading CoverageState = "loading"
	CoverageReady   CoverageState = "ready"
	CoverageStale   CoverageState = "stale"  // rows are the last known
	CoverageDenied  CoverageState = "denied" // no permission to observe it
	CoverageError   CoverageState = "error"
)

// Equal compares statuses, coverage included.
func (s ViewStatus) Equal(o ViewStatus) bool {
	if s.State != o.State || s.Class != o.Class || s.Message != o.Message || len(s.Coverage) != len(o.Coverage) {
		return false
	}
	for i := range s.Coverage {
		if s.Coverage[i] != o.Coverage[i] {
			return false
		}
	}
	return true
}

// Clone copies the status so the copy shares nothing with it.
func (s ViewStatus) Clone() ViewStatus {
	if s.Coverage != nil {
		s.Coverage = append([]SourceCoverage(nil), s.Coverage...)
	}
	return s
}

// Error is a classified provider error. Message is its English text; Why,
// when the sentence is the provider's own (not a server's text), is that
// sentence by key for the UI to say in its language.
type Error struct {
	Class   ErrorClass
	Message string
	Why     *core.Message
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Message }

// Said is an error whose text is the provider's sentence m.
func Said(class ErrorClass, m core.Message) *Error {
	if m.Params != nil {
		params := make(map[string]string, len(m.Params))
		for k, v := range m.Params {
			params[k] = v
		}
		m.Params = params
	}
	return &Error{Class: class, Message: m.Text, Why: &m}
}

// MetricsSource is implemented by sessions that can report resource usage
// for a query's rows (k8s: metrics.k8s.io for pods and nodes; Compose: the
// stats of containers).
type MetricsSource interface {
	// Metrics returns usage keyed by row id for rowIDs — the rows the page
	// shows now (a provider may return more; the API keeps those asked
	// for). Samples are attributed only when the provider can tell which
	// incarnation they belong to. A missing metrics API is
	// *Error{ClassUnsupported}; objects without a sample are absent from
	// Values (unknown, never zero). ctx ends when the page stops waiting:
	// the caller must not be kept waiting (work shared with other callers
	// may go on).
	Metrics(ctx context.Context, q Query, rowIDs []string) (Metrics, error)
}

type Metrics struct {
	Timestamp time.Time        `json:"timestamp"`
	Window    string           `json:"window,omitempty"`
	Values    map[string]Usage `json:"-"`
}

// Usage: CPU in cores, Memory in bytes; nil — that metric is unknown (the
// other may be known). A partial value is a sum over parts of which some
// did not answer (or were not asked): at least that much.
type Usage struct {
	CPU           *float64  `json:"cpu,omitempty"`
	Memory        *float64  `json:"memory,omitempty"`
	CPUPartial    bool      `json:"cpuPartial,omitempty"`
	MemoryPartial bool      `json:"memoryPartial,omitempty"`
	At            time.Time `json:"at,omitzero"` // when the sample was taken
}

// Num is a known metric value.
func Num(v float64) *float64 { return &v }
