package agentgrant

import (
	"fmt"
	"slices"
	"strings"
)

// Group is an independently switchable, named collection of additive grants.
// Its grants belong to exactly one scope. Disabled groups retain their contents.
type Group struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Scope    Scope   `json:"scope"`
	Disabled bool    `json:"disabled,omitempty"`
	Grants   []Grant `json:"grants"`
}

// HasConfiguration includes paused scopes and empty groups: neither is revocation.
func (t Target) HasConfiguration() bool {
	return len(t.Grants)+len(t.Groups)+len(t.DisabledScopes) > 0
}

// ScopeDisabled is a master stop, including grants inherited from ScopeAll.
// A disabled All switch pauses every namespace, but not cluster-scoped objects.
func (t Target) ScopeDisabled(scope string, scoped bool) bool {
	for _, s := range t.DisabledScopes {
		if !scoped && s.Mode == ScopeCluster || scoped && (s.Mode == ScopeAll || s.Mode == ScopeOne && s.Name == scope) {
			return true
		}
	}
	return false
}

// EffectiveGrants preserves distinct rows: merging kind lists could turn an
// all-kinds grant into a destructive or sensitive-kind grant by accident.
func (t Target) EffectiveGrants() []Grant {
	out := make([]Grant, 0, len(t.Grants))
	add := func(gs []Grant) {
		for _, g := range gs {
			if !t.ScopeDisabled(g.Scope.Name, g.Scope.Mode != ScopeCluster) {
				out = append(out, g)
			}
		}
	}
	add(t.Grants) // legacy rows remain active until explicitly replaced
	for _, g := range t.Groups {
		if !g.Disabled {
			add(g.Grants)
		}
	}
	return out
}

// Validate validates the entire replacement before any database write.
func (t Target) Validate() error {
	seen := map[string]bool{}
	for _, g := range t.Grants {
		if err := g.Validate(); err != nil {
			return err
		}
	}
	for _, group := range t.Groups {
		if group.ID == "" || len(group.ID) > 128 || seen[group.ID] {
			return fmt.Errorf("group ids must be nonempty, unique and at most 128 bytes")
		}
		seen[group.ID] = true
		if strings.TrimSpace(group.Name) == "" || len([]rune(group.Name)) > 128 {
			return fmt.Errorf("group names must contain 1..128 characters")
		}
		if err := (Grant{Scope: group.Scope, Verb: VerbRead}).Validate(); err != nil {
			return err
		}
		verbs := map[string]bool{}
		for _, g := range group.Grants {
			if verbs[g.Verb] {
				return fmt.Errorf("group %q repeats verb %q; use separate groups", group.ID, g.Verb)
			}
			verbs[g.Verb] = true
			if g.Scope != group.Scope {
				return fmt.Errorf("group %q contains a grant for another scope", group.ID)
			}
			if err := g.Validate(); err != nil {
				return err
			}
		}
	}
	for i, scope := range t.DisabledScopes {
		if err := (Grant{Scope: scope, Verb: VerbRead}).Validate(); err != nil {
			return err
		}
		if slices.Contains(t.DisabledScopes[:i], scope) {
			return fmt.Errorf("duplicate disabled scope")
		}
	}
	return nil
}
