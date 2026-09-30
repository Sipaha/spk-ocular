package agentgrant

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func one(ns, verb string, kinds ...string) Grant {
	g := Grant{Scope: Scope{Mode: ScopeOne, Name: ns}, Verb: verb}
	if len(kinds) > 0 {
		g.Kinds = kinds
	}
	return g
}

func TestValidate(t *testing.T) {
	ok := []Grant{
		one("a", VerbRead),
		one("a", VerbLogs, "pods"),
		one("a", VerbEdit, "configmaps"),
		one("a", "action:restart", "apps/deployments"),
		{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead},
		{Scope: Scope{Mode: ScopeCluster}, Verb: VerbRead, Kinds: []string{"nodes"}},
	}
	for _, g := range ok {
		assert.NoError(t, g.Validate(), "%+v", g)
	}
	bad := map[string]Grant{
		"no scope name":        {Scope: Scope{Mode: ScopeOne}, Verb: VerbRead},
		"a name with all":      {Scope: Scope{Mode: ScopeAll, Name: "a"}, Verb: VerbRead},
		"a name with cluster":  {Scope: Scope{Mode: ScopeCluster, Name: "a"}, Verb: VerbRead},
		"unknown mode":         {Scope: Scope{Mode: "some"}, Verb: VerbRead},
		"cluster writes":       {Scope: Scope{Mode: ScopeCluster}, Verb: VerbEdit},
		"cluster actions":      {Scope: Scope{Mode: ScopeCluster}, Verb: "action:cordon"},
		"cluster logs":         {Scope: Scope{Mode: ScopeCluster}, Verb: VerbLogs},
		"unknown verb":         one("a", "exec"),
		"action without an id": one("a", "action:"),
		"empty kinds list":     {Scope: Scope{Mode: ScopeOne, Name: "a"}, Verb: VerbRead, Kinds: []string{}},
		"an empty kind":        one("a", VerbRead, ""),
	}
	for name, g := range bad {
		assert.Error(t, g.Validate(), name)
	}
}

func TestAllows(t *testing.T) {
	type tc struct {
		name   string
		grants []Grant
		req    Request
		ok     bool
		noConf bool
	}
	read := func(ns, kind string) Request {
		return Request{Scope: ns, Scoped: true, Kind: kind, Verb: VerbRead}
	}
	cases := []tc{
		{"nothing granted", nil, read("a", "pods"), false, false},
		{"read in the namespace", []Grant{one("a", VerbRead)}, read("a", "pods"), true, false},
		{"not another namespace", []Grant{one("a", VerbRead)}, read("b", "pods"), false, false},
		{"all namespaces", []Grant{{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead}}, read("kube-system", "pods"), true, false},
		{"a scoped kind needs a scope", []Grant{{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead}}, read("", "pods"), false, false},
		{"kinds listed", []Grant{one("a", VerbRead, "pods")}, read("a", "secrets"), false, false},
		{"all kinds read a Secret's (masked) object", []Grant{one("a", VerbRead)}, Request{Scope: "a", Scoped: true, Kind: "secrets", Sensitive: true, Verb: VerbRead}, true, false},
		{"all kinds never edit a sensitive kind", []Grant{one("a", VerbEdit)}, Request{Scope: "a", Scoped: true, Kind: "secrets", Sensitive: true, Verb: VerbEdit}, false, false},
		{"a sensitive kind edited by name", []Grant{one("a", VerbEdit, "secrets")}, Request{Scope: "a", Scoped: true, Kind: "secrets", Sensitive: true, Verb: VerbEdit}, true, false},
		{"all kinds edit a plain one", []Grant{one("a", VerbEdit)}, Request{Scope: "a", Scoped: true, Kind: "configmaps", Verb: VerbEdit}, true, false},
		{"destructive needs the kind named", []Grant{one("a", "action:scale")}, Request{Scope: "a", Scoped: true, Kind: "apps/deployments", Verb: "action:scale", Destructive: true}, false, false},
		{"destructive by name", []Grant{one("a", "action:scale", "apps/deployments")}, Request{Scope: "a", Scoped: true, Kind: "apps/deployments", Verb: "action:scale", Destructive: true}, true, false},
		{"destructive by name without confirmation", []Grant{func() Grant { g := one("a", "action:delete", "pods"); g.NoConfirm = true; return g }()}, Request{Scope: "a", Scoped: true, Kind: "pods", Verb: "action:delete", Destructive: true}, true, true},
		{"delete of all kinds never runs", []Grant{one("a", "action:delete")}, Request{Scope: "a", Scoped: true, Kind: "pods", Verb: "action:delete", Destructive: true}, false, false},
		{"a destructive edit needs the kind named", []Grant{one("a", VerbEdit)}, Request{Scope: "a", Scoped: true, Kind: "configmaps", Verb: VerbEdit, Destructive: true}, false, false},
		{"restart does not give delete", []Grant{one("a", "action:restart", "pods")}, Request{Scope: "a", Scoped: true, Kind: "pods", Verb: "action:delete", Destructive: true}, false, false},
		{"read does not give logs", []Grant{one("a", VerbRead)}, Request{Scope: "a", Scoped: true, Kind: "pods", Verb: VerbLogs}, false, false},
		{"cluster read", []Grant{{Scope: Scope{Mode: ScopeCluster}, Verb: VerbRead}}, Request{Kind: "nodes", Verb: VerbRead}, true, false},
		{"all namespaces are not the cluster", []Grant{{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead}}, Request{Kind: "nodes", Verb: VerbRead}, false, false},
		{"a namespace is not the cluster", []Grant{one("a", VerbRead)}, Request{Scope: "a", Kind: "nodes", Verb: VerbRead}, false, false},
		{"cluster objects are never changed", []Grant{{Scope: Scope{Mode: ScopeCluster}, Verb: VerbRead}, {Scope: Scope{Mode: ScopeAll}, Verb: "action:cordon"}}, Request{Kind: "nodes", Verb: "action:cordon"}, false, false},
		{"cluster read of listed kinds", []Grant{{Scope: Scope{Mode: ScopeCluster}, Verb: VerbRead, Kinds: []string{"nodes"}}}, Request{Kind: "namespaces", Verb: VerbRead}, false, false},
		{"no confirmation is the named grant's", []Grant{func() Grant { g := one("a", "action:scale"); g.NoConfirm = true; return g }(), one("a", "action:scale", "apps/deployments")}, Request{Scope: "a", Scoped: true, Kind: "apps/deployments", Verb: "action:scale", Destructive: true}, true, false},
	}
	for _, c := range cases {
		d := Allows(c.grants, c.req)
		assert.Equal(t, c.ok, d.OK, c.name)
		assert.Equal(t, c.noConf, d.NoConfirm, c.name)
		if !d.OK {
			assert.NotEmpty(t, d.Reason, c.name)
		}
	}
}

func TestReadable(t *testing.T) {
	gs := []Grant{one("b", VerbRead), one("a", VerbRead, "pods"), one("c", VerbLogs), {Scope: Scope{Mode: ScopeCluster}, Verb: VerbRead, Kinds: []string{"nodes"}}}
	names, all := ReadableScopes(gs, "pods")
	assert.Equal(t, []string{"a", "b"}, names)
	assert.False(t, all)
	names, _ = ReadableScopes(gs, "secrets")
	assert.Equal(t, []string{"b"}, names)
	_, all = ReadableScopes(append(gs, Grant{Scope: Scope{Mode: ScopeAll}, Verb: VerbRead}), "pods")
	assert.True(t, all)
	assert.True(t, ClusterReadable(gs, "nodes"))
	assert.False(t, ClusterReadable(gs, "namespaces"))
}

// Two named grants of the same verb: either one's "no confirmation" holds.
func TestNoConfirmOfAnyNamedGrant(t *testing.T) {
	nc := one("a", "action:delete", "pods")
	nc.NoConfirm = true
	for _, gs := range [][]Grant{{one("a", "action:delete", "pods"), nc}, {nc, one("a", "action:delete", "pods")}} {
		d := Allows(gs, Request{Scope: "a", Scoped: true, Kind: "pods", Verb: "action:delete", Destructive: true})
		assert.True(t, d.OK)
		assert.True(t, d.NoConfirm)
	}
}
