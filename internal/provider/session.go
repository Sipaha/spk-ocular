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

// Query is what a view shows. It is immutable for the view's lifetime.
type Query struct {
	Kind  string        `json:"kind"`
	Scope core.ScopeSel `json:"scope"`
	// Subject narrows the kind to objects about one object (events of a pod).
	// Providers translate it (k8s: involvedObject.uid); nil = no narrowing.
	Subject *core.Ref `json:"subject,omitempty"`
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
	ClassNotFound     ErrorClass = "not_found"
	ClassUnsupported  ErrorClass = "unsupported"
	ClassConflict     ErrorClass = "conflict"
	ClassInternal     ErrorClass = "internal"
)

type ViewStatus struct {
	State   StatusState `json:"state"`
	Class   ErrorClass  `json:"class,omitempty"`
	Message string      `json:"message,omitempty"`
}

// Error is a classified provider error.
type Error struct {
	Class   ErrorClass
	Message string
}

func (e *Error) Error() string { return string(e.Class) + ": " + e.Message }

// MetricsSource is implemented by sessions that can report resource usage
// for a query's rows (k8s: metrics.k8s.io for pods and nodes).
type MetricsSource interface {
	// Metrics returns usage keyed by "scope/name" (scope "" for unscoped).
	// A missing metrics API is *Error{ClassUnsupported}; objects without a
	// sample are absent from Values (unknown, never zero).
	Metrics(ctx context.Context, q Query) (Metrics, error)
}

type Metrics struct {
	Timestamp time.Time        `json:"timestamp"`
	Window    string           `json:"window,omitempty"`
	Values    map[string]Usage `json:"-"`
}

// Usage: CPU in cores, Memory in bytes.
type Usage struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}
