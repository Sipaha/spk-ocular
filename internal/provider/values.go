package provider

import (
	"context"

	"github.com/spk/spk-ocular/internal/core"
)

// ValueHolder keeps protected values by key (KindDescriptor.Values): keys
// with sizes, one value on request, a key's change through a review and
// one write. A value leaves only in RevealValue's answer: never in a base,
// grant, plan, result or error. The API signs ValueBase and ValueGrant.
type ValueHolder interface {
	// Values lists the object's keys (fresh read, nothing cached).
	Values(ctx context.Context, ref core.Ref) (core.ValueList, ValueBase, error)
	// RevealValue reads one key's value now (ref.UID: that object only).
	RevealValue(ctx context.Context, ref core.Ref, key string) (core.Value, error)
	// PrepareValueEdit reads what the edit would do; the grant is nil when
	// nothing can be written.
	PrepareValueEdit(ctx context.Context, req ValueEditRequest) (core.ValuePlan, *ValueGrant, error)
	// RunValueEdit writes a confirmed plan once (no retries).
	RunValueEdit(ctx context.Context, run ValueEditRun) (core.ValueResult, error)
}

// ValuePrint is a key's presence and keyed fingerprint (an HMAC under a
// process key — a signed value can be decoded, a plain hash of a short
// value could be guessed).
type ValuePrint struct {
	Key     string `json:"key"`
	Present bool   `json:"present"`
	Print   string `json:"print,omitempty"`
}

// ValueBase is the object and keys a value edit starts from.
type ValueBase struct {
	Route     string       `json:"route"`
	Namespace string       `json:"namespace,omitempty"`
	Name      string       `json:"name"`
	UID       string       `json:"uid"`
	Version   string       `json:"version"`
	Keys      []ValuePrint `json:"keys"`
}

// ValueGrant is what a plan allows to write: its digest binds the final
// write (route, object, version, key, operation, presence, bytes); Expected
// is every key as the review left it, ServerChanges what the review
// changes of the edit — for the write's check, without state.
type ValueGrant struct {
	Route         string       `json:"route"`
	Namespace     string       `json:"namespace,omitempty"`
	Name          string       `json:"name"`
	UID           string       `json:"uid"`
	Version       string       `json:"version"`
	Digest        string       `json:"digest"`
	Expected      []ValuePrint `json:"expected"`
	ServerChanges []string     `json:"serverChanges,omitempty"`
	Mode          string       `json:"mode"`
}

// ValueEditRequest is a key's change to plan: set it to Value (decoded
// bytes; empty is a value) or delete it.
type ValueEditRequest struct {
	Ref   core.Ref
	Base  ValueBase
	Key   string
	Op    string
	Value []byte
}

// ValueEditRun is a confirmed plan.
type ValueEditRun struct {
	ValueEditRequest
	Grant ValueGrant
}
