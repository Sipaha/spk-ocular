package agentgrant

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestGroupUnionNeverNarrows(t *testing.T) {
	scope := Scope{Mode: ScopeOne, Name: "a"}
	broad := Grant{Scope: scope, Verb: VerbRead}
	narrow := Grant{Scope: scope, Verb: VerbRead, Kinds: []string{"pods"}}
	target := Target{Groups: []Group{
		{ID: "base", Name: "Base", Scope: scope, Grants: []Grant{broad}},
		{ID: "extra", Name: "Extra", Scope: scope, Grants: []Grant{narrow}},
	}}
	require.NoError(t, target.Validate())
	assert.True(t, Allows(target.EffectiveGrants(), Request{Scoped: true, Scope: "a", Kind: "secrets", Verb: VerbRead}).OK)
	target.Groups[1].Disabled = true
	assert.True(t, Allows(target.EffectiveGrants(), Request{Scoped: true, Scope: "a", Kind: "secrets", Verb: VerbRead}).OK)
	target.Groups[0].Disabled = true
	assert.Empty(t, target.EffectiveGrants())
	assert.True(t, target.HasConfiguration())
	target.Groups[1].Disabled = false
	assert.False(t, Allows(target.EffectiveGrants(), Request{Scoped: true, Scope: "a", Kind: "secrets", Verb: VerbRead}).OK)
	assert.True(t, Allows(target.EffectiveGrants(), Request{Scoped: true, Scope: "a", Kind: "pods", Verb: VerbRead}).OK)
}

func TestGroupUnionKeepsConfirmationPerKind(t *testing.T) {
	scope := Scope{Mode: ScopeOne, Name: "a"}
	target := Target{Groups: []Group{
		{ID: "normal", Name: "Normal", Scope: scope, Grants: []Grant{{Scope: scope, Verb: "action:delete", Kinds: []string{"pods", "services"}}}},
		{ID: "fast", Name: "Fast", Scope: scope, Grants: []Grant{{Scope: scope, Verb: "action:delete", Kinds: []string{"pods"}, NoConfirm: true}}},
	}}
	gs := target.EffectiveGrants()
	assert.True(t, Allows(gs, Request{Scoped: true, Scope: "a", Kind: "pods", Verb: "action:delete", Destructive: true}).NoConfirm)
	assert.False(t, Allows(gs, Request{Scoped: true, Scope: "a", Kind: "services", Verb: "action:delete", Destructive: true}).NoConfirm)
	target.Groups[1].Disabled = true
	assert.False(t, Allows(target.EffectiveGrants(), Request{Scoped: true, Scope: "a", Kind: "pods", Verb: "action:delete", Destructive: true}).NoConfirm)
}

func TestScopeMasterStopsInheritedAccess(t *testing.T) {
	target := Target{Grants: []Grant{{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead}}, DisabledScopes: []Scope{{Mode: ScopeOne, Name: "a"}}}
	assert.True(t, target.ScopeDisabled("a", true))
	assert.False(t, target.ScopeDisabled("b", true))
	assert.False(t, target.ScopeDisabled("", false))
	// All remains usable for other namespaces. Each object also checks its master.
	assert.Len(t, target.EffectiveGrants(), 1)
	target.DisabledScopes = []Scope{{Mode: ScopeAll}}
	assert.Empty(t, target.EffectiveGrants())
	assert.True(t, target.ScopeDisabled("b", true))
}

func TestInvalidGroupsRejected(t *testing.T) {
	scope := Scope{Mode: ScopeOne, Name: "a"}
	valid := Group{ID: "g", Name: "Maintenance", Scope: scope, Grants: []Grant{one("a", VerbRead)}}
	for name, groups := range map[string][]Group{
		"duplicate id":  {valid, valid},
		"blank name":    {{ID: "g", Name: " ", Scope: scope}},
		"other scope":   {{ID: "g", Name: "G", Scope: scope, Grants: []Grant{one("b", VerbRead)}}},
		"repeated verb": {{ID: "g", Name: "G", Scope: scope, Grants: []Grant{one("a", VerbRead), one("a", VerbRead, "pods")}}},
	} {
		t.Run(name, func(t *testing.T) { assert.Error(t, (Target{Groups: groups}).Validate()) })
	}
}
