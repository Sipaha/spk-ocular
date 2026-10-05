// Package agentgrant is the model of what the user grants local agents
// (docs/agent-api.md): rows of (scope,
// verb, kinds, no confirmation) per target, and the pure decisions over
// them. No I/O: the store keeps the rows, agentapi asks the decisions.
package agentgrant

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ScopeMode says which scopes a grant covers.
type ScopeMode string

const (
	// ScopeOne: the namespace / project Name.
	ScopeOne ScopeMode = "one"
	// ScopeAll: every namespace, those created later included.
	ScopeAll ScopeMode = "all"
	// ScopeCluster: the objects outside namespaces (read only).
	ScopeCluster ScopeMode = "cluster"
)

type Scope struct {
	Mode ScopeMode `json:"mode"`
	Name string    `json:"name,omitempty"`
}

// Verbs. An action is "action:<id>" (the kind's action id).
const (
	VerbRead     = "read"
	VerbLogs     = "logs"
	VerbEdit     = "edit"
	ActionPrefix = "action:"
)

// ActionVerb is the verb of the kind's action id.
func ActionVerb(id string) string { return ActionPrefix + id }

// Grant is one verb in one scope of a target.
type Grant struct {
	Scope Scope  `json:"scope"`
	Verb  string `json:"verb"`
	// Kinds are the kind ids the verb covers; nil: every kind (not the
	// sensitive ones for edit, and nothing destructive).
	Kinds []string `json:"kinds"`
	// NoConfirm: a destructive plan runs without the user's confirmation.
	NoConfirm bool `json:"noConfirm,omitempty"`
}

// Target is a target's grants and the identity they were given for.
type Target struct {
	Provider string `json:"provider"`
	Target   string `json:"target"`
	// Title is the target's title when granted (it may be gone since).
	Title string `json:"title"`
	// Identity is what the target pointed at when granted
	// (core.Target.Identity plus the session's part).
	Identity string `json:"identity"`
	// Observed: another identity a call saw; the grants are suspended
	// until the user confirms them for it.
	Observed string  `json:"observed,omitempty"`
	Grants   []Grant `json:"grants"`
}

// Suspended: the target now points elsewhere than when granted.
func (t Target) Suspended() bool { return t.Observed != "" }

// Validate checks one grant's shape.
func (g Grant) Validate() error {
	switch g.Scope.Mode {
	case ScopeOne:
		if g.Scope.Name == "" {
			return errors.New("a namespace grant needs the namespace")
		}
	case ScopeAll, ScopeCluster:
		if g.Scope.Name != "" {
			return fmt.Errorf("a %s grant names no namespace", g.Scope.Mode)
		}
	default:
		return fmt.Errorf("unknown scope mode %q", g.Scope.Mode)
	}
	switch {
	case g.Verb == VerbRead, g.Verb == VerbLogs, g.Verb == VerbEdit:
	case strings.HasPrefix(g.Verb, ActionPrefix) && len(g.Verb) > len(ActionPrefix):
	default:
		return fmt.Errorf("unknown verb %q", g.Verb)
	}
	if g.Scope.Mode == ScopeCluster && g.Verb != VerbRead {
		return errors.New("objects outside namespaces can only be read")
	}
	if g.Kinds != nil && len(g.Kinds) == 0 {
		return errors.New("no kinds: leave the list out for all kinds")
	}
	if slices.Contains(g.Kinds, "") {
		return errors.New("an empty kind")
	}
	return nil
}

// Request is one check: a verb on an object of a kind in a scope.
type Request struct {
	// Scope is the object's namespace / project ("" outside them).
	Scope string
	// Scoped: the kind lives in scopes (core.KindDescriptor.Scoped).
	Scoped bool
	Kind   string
	// Sensitive: core.KindDescriptor.Sensitive.
	Sensitive bool
	Verb      string
	// Destructive: the plan is (core.ActionPlan/EditPlan.Destructive).
	Destructive bool
}

// Decision: OK, or why not (English, for the agent). NoConfirm: a
// destructive plan runs without the user's confirmation.
type Decision struct {
	OK        bool
	NoConfirm bool
	Reason    string
}

func (s Scope) covers(scope string) bool {
	switch s.Mode {
	case ScopeOne:
		return scope != "" && s.Name == scope
	case ScopeAll:
		return scope != ""
	}
	return false
}

// Allows decides r against a target's grants.
func Allows(gs []Grant, r Request) Decision {
	if !r.Scoped {
		if r.Verb != VerbRead {
			return Decision{Reason: fmt.Sprintf("%s objects are outside namespaces: agents may only read them", r.Kind)}
		}
		for _, g := range gs {
			if g.Scope.Mode == ScopeCluster && g.Verb == VerbRead && (g.Kinds == nil || slices.Contains(g.Kinds, r.Kind)) {
				return Decision{OK: true}
			}
		}
		return Decision{Reason: fmt.Sprintf("reading %s (objects outside namespaces) is not granted", r.Kind)}
	}
	if r.Scope == "" {
		return Decision{Reason: fmt.Sprintf("%s live in namespaces: name one", r.Kind)}
	}
	var sawAllKinds bool
	var d Decision
	for _, g := range gs {
		if g.Verb != r.Verb || !g.Scope.covers(r.Scope) {
			continue
		}
		switch {
		case slices.Contains(g.Kinds, r.Kind):
			d.OK = true
			d.NoConfirm = d.NoConfirm || g.NoConfirm && r.Destructive
		case g.Kinds == nil:
			sawAllKinds = true
			sensitiveEdit := r.Sensitive && r.Verb == VerbEdit
			d.OK = d.OK || !r.Destructive && !sensitiveEdit
		}
	}
	switch {
	case d.OK:
		return d
	case sawAllKinds && r.Destructive:
		return Decision{Reason: fmt.Sprintf("%s on %s is destructive here: it is granted only with the kind named", r.Verb, r.Kind)}
	case sawAllKinds && r.Sensitive:
		return Decision{Reason: fmt.Sprintf("editing %s is granted only with the kind named", r.Kind)}
	}
	return Decision{Reason: fmt.Sprintf("%s on %s in %s is not granted", r.Verb, r.Kind, r.Scope)}
}

// ReadableScopes: the namespaces whose objects of kind may be read, sorted,
// or all (every namespace).
func ReadableScopes(gs []Grant, kind string) (names []string, all bool) {
	seen := map[string]bool{}
	for _, g := range gs {
		if g.Verb != VerbRead || g.Kinds != nil && !slices.Contains(g.Kinds, kind) {
			continue
		}
		switch g.Scope.Mode {
		case ScopeAll:
			all = true
		case ScopeOne:
			if !seen[g.Scope.Name] {
				seen[g.Scope.Name] = true
				names = append(names, g.Scope.Name)
			}
		}
	}
	sort.Strings(names)
	return names, all
}

// ClusterReadable: objects of kind outside namespaces may be read.
func ClusterReadable(gs []Grant, kind string) bool {
	return Allows(gs, Request{Kind: kind, Verb: VerbRead}).OK
}
