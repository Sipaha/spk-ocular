package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spk/spk-ocular/internal/agentgrant"
)

func grantOne(ns, verb string, kinds ...string) agentgrant.Grant {
	g := agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: ns}, Verb: verb}
	if len(kinds) > 0 {
		g.Kinds = kinds
	}
	return g
}

func TestAgentGrantsReplacedPerTarget(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	nc := grantOne("a", "action:delete", "pods")
	nc.NoConfirm = true
	a := agentgrant.Target{Provider: "kubernetes", Target: "ctx-a", Title: "A", Identity: "https://a | ca:1 | user:u",
		Grants: []agentgrant.Grant{grantOne("a", agentgrant.VerbRead), nc, {Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: agentgrant.VerbRead, Kinds: []string{"nodes"}}}}
	b := agentgrant.Target{Provider: "compose", Target: "context:default", Title: "default", Identity: "unix:///run/docker.sock | daemon:X",
		Grants: []agentgrant.Grant{grantOne("shop", agentgrant.VerbLogs)}}
	require.NoError(t, s.ReplaceAgentGrants(ctx, a))
	require.NoError(t, s.ReplaceAgentGrants(ctx, b))
	got, err := s.AgentTargets(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, b, got[0], "ordered by provider, target")
	assert.Equal(t, a, got[1])

	// Replaced whole; the identity granted stays (only a reconfirmation
	// moves it), the title follows.
	a2 := a
	a2.Title, a2.Identity = "A2", "https://elsewhere"
	a2.Grants = []agentgrant.Grant{grantOne("b", agentgrant.VerbEdit, "configmaps")}
	require.NoError(t, s.ReplaceAgentGrants(ctx, a2))
	got, _ = s.AgentTargets(ctx)
	assert.Equal(t, "A2", got[1].Title)
	assert.Equal(t, a.Identity, got[1].Identity)
	assert.Equal(t, a2.Grants, got[1].Grants)

	// No grants: the target goes.
	a2.Grants = nil
	require.NoError(t, s.ReplaceAgentGrants(ctx, a2))
	got, _ = s.AgentTargets(ctx)
	assert.Equal(t, []agentgrant.Target{b}, got)

	require.NoError(t, s.RevokeAllAgentGrants(ctx))
	got, _ = s.AgentTargets(ctx)
	assert.Empty(t, got)
}

func TestAnInvalidGrantChangesNothing(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	a := agentgrant.Target{Provider: "kubernetes", Target: "ctx-a", Identity: "i", Grants: []agentgrant.Grant{grantOne("a", agentgrant.VerbRead)}}
	require.NoError(t, s.ReplaceAgentGrants(ctx, a))
	bad := a
	bad.Grants = []agentgrant.Grant{grantOne("b", agentgrant.VerbRead), {Scope: agentgrant.Scope{Mode: agentgrant.ScopeCluster}, Verb: agentgrant.VerbEdit}}
	require.Error(t, s.ReplaceAgentGrants(ctx, bad))
	got, _ := s.AgentTargets(ctx)
	assert.Equal(t, []agentgrant.Target{a}, got)
	bad.Grants = []agentgrant.Grant{grantOne("b", "exec")} // the schema has no list of verbs: the model's check
	require.Error(t, s.ReplaceAgentGrants(ctx, bad))
	got, _ = s.AgentTargets(ctx)
	assert.Equal(t, []agentgrant.Target{a}, got)
	require.Error(t, s.ReplaceAgentGrants(ctx, agentgrant.Target{Provider: "kubernetes", Target: "ctx-b", Grants: a.Grants}), "a target without identity")
}

// The database refuses a cluster write even past the model's check.
func TestTheSchemaRefusesClusterWrites(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, s.ReplaceAgentGrants(ctx, agentgrant.Target{Provider: "k", Target: "t", Identity: "i", Grants: []agentgrant.Grant{grantOne("a", agentgrant.VerbRead)}}))
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_grants(provider, target, scope_mode, scope_name, verb, kinds, no_confirm) VALUES ('k', 't', 'cluster', '', 'edit', NULL, 0)`)
	require.Error(t, err)
}

func TestAgentIdentityObservedAndReconfirmed(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	a := agentgrant.Target{Provider: "k", Target: "t", Identity: "one", Grants: []agentgrant.Grant{grantOne("a", agentgrant.VerbRead)}}
	require.NoError(t, s.ReplaceAgentGrants(ctx, a))

	changed, err := s.ObserveAgentIdentity(ctx, "k", "t", "one")
	require.NoError(t, err)
	assert.False(t, changed)
	changed, _ = s.ObserveAgentIdentity(ctx, "k", "t", "two")
	assert.True(t, changed)
	changed, _ = s.ObserveAgentIdentity(ctx, "k", "t", "two")
	assert.False(t, changed, "said once")
	got, _ := s.AgentTargets(ctx)
	assert.True(t, got[0].Suspended())
	assert.Equal(t, "two", got[0].Observed)

	// Pointed back where it was granted: the grants hold again.
	changed, _ = s.ObserveAgentIdentity(ctx, "k", "t", "one")
	assert.True(t, changed)
	got, _ = s.AgentTargets(ctx)
	assert.False(t, got[0].Suspended())

	_, _ = s.ObserveAgentIdentity(ctx, "k", "t", "two")
	_, _ = s.ObserveAgentIdentity(ctx, "k", "t", "three")
	// The user saw "two": what it points at now was never shown to them.
	ok, err := s.ReconfirmAgentTarget(ctx, "k", "t", "two")
	require.NoError(t, err)
	assert.False(t, ok)
	got, _ = s.AgentTargets(ctx)
	assert.Equal(t, "one", got[0].Identity)
	assert.True(t, got[0].Suspended())
	_, _ = s.ObserveAgentIdentity(ctx, "k", "t", "two")
	ok, err = s.ReconfirmAgentTarget(ctx, "k", "t", "two")
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = s.AgentTargets(ctx)
	assert.Equal(t, "two", got[0].Identity)
	assert.False(t, got[0].Suspended())

	changed, err = s.ObserveAgentIdentity(ctx, "k", "gone", "x")
	require.NoError(t, err)
	assert.False(t, changed, "no grants, nothing to suspend")
}

func TestAuditAggregatesReadsPerMinute(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	at := time.Date(2026, 9, 30, 10, 0, 5, 0, time.UTC)
	read := AuditEntry{At: at, Agent: "claude", Method: "ListObjects", Provider: "k", Target: "t", Scope: "a", Phase: AuditRead, Outcome: "done"}
	require.NoError(t, s.AppendAudit(ctx, read))
	read.At = at.Add(30 * time.Second)
	read.Scope = "b"
	require.NoError(t, s.AppendAudit(ctx, read))
	read.At = at.Add(70 * time.Second) // the next minute
	require.NoError(t, s.AppendAudit(ctx, read))
	other := read
	other.Agent = "codex"
	require.NoError(t, s.AppendAudit(ctx, other))
	run := AuditEntry{At: at.Add(80 * time.Second), Agent: "claude", Method: "RunAction", Provider: "k", Target: "t", Scope: "a", Object: "apps/deployments/a/web", Verb: "action:restart", ExpectHash: "abc", Phase: AuditIntent}
	require.NoError(t, s.AppendAudit(ctx, run))
	run.Phase, run.Outcome = AuditOutcome, "done"
	require.NoError(t, s.AppendAudit(ctx, run))
	require.NoError(t, s.AppendAudit(ctx, run), "outcomes are never folded")

	all, err := s.ListAudit(ctx, AuditFilter{Limit: 100})
	require.NoError(t, err)
	require.Len(t, all, 6)
	assert.Equal(t, AuditOutcome, all[0].Phase, "newest first")
	counts := map[string]int{}
	for _, e := range all {
		if e.Phase == AuditRead {
			counts[fmt.Sprintf("%s@%d", e.Agent, e.At.Unix()/60)] += e.Count
		}
	}
	assert.Equal(t, map[string]int{
		fmt.Sprintf("claude@%d", at.Unix()/60):   2,
		fmt.Sprintf("claude@%d", at.Unix()/60+1): 1,
		fmt.Sprintf("codex@%d", at.Unix()/60+1):  1,
	}, counts)

	mine, _ := s.ListAudit(ctx, AuditFilter{Agent: "codex", Limit: 100})
	require.Len(t, mine, 1)
	page, _ := s.ListAudit(ctx, AuditFilter{Limit: 2})
	require.Len(t, page, 2)
	rest, _ := s.ListAudit(ctx, AuditFilter{Before: page[1].ID, Limit: 100})
	assert.Len(t, rest, 4)
	none, _ := s.ListAudit(ctx, AuditFilter{Provider: "k", Target: "other", Limit: 100})
	assert.Empty(t, none)
}

// A folded read names its object (and scope) only when every read of it
// did; its time is the minute's first read, so the order by record stays
// the order in time.
func TestAFoldedReadOfSeveralObjectsNamesNone(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	at := time.Date(2026, 9, 30, 10, 0, 5, 0, time.UTC)
	read := AuditEntry{At: at, Agent: "claude", Method: "GetObject", Provider: "k", Target: "t", Scope: "a", Object: "pods/a/one", Phase: AuditRead, Outcome: "done"}
	require.NoError(t, s.AppendAudit(ctx, read))
	same := read
	same.Method, same.At = "GetLogs", at.Add(time.Second)
	require.NoError(t, s.AppendAudit(ctx, same))
	same.At = at.Add(2 * time.Second)
	require.NoError(t, s.AppendAudit(ctx, same))
	read.At, read.Object = at.Add(30*time.Second), "pods/a/two"
	require.NoError(t, s.AppendAudit(ctx, read))
	read.At, read.Scope, read.Object = at.Add(40*time.Second), "b", "pods/b/three"
	require.NoError(t, s.AppendAudit(ctx, read))

	all, err := s.ListAudit(ctx, AuditFilter{Limit: 10})
	require.NoError(t, err)
	require.Len(t, all, 2)
	logs, objects := all[0], all[1]
	assert.Equal(t, "GetLogs", logs.Method)
	assert.Equal(t, 2, logs.Count)
	assert.Equal(t, "pods/a/one", logs.Object, "every read of it named it")
	assert.Equal(t, 3, objects.Count)
	assert.Equal(t, AuditSeveral, objects.Object, "several objects")
	assert.Equal(t, AuditSeveral, objects.Scope, "several scopes")
	assert.Equal(t, at, objects.At.UTC(), "the minute's first read")
}

func TestAuditRotates(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	oldKeep, oldEvery := auditKeep, auditRotateEvery
	auditKeep, auditRotateEvery = 10, 5
	t.Cleanup(func() { auditKeep, auditRotateEvery = oldKeep, oldEvery })
	for i := range 40 {
		require.NoError(t, s.AppendAudit(ctx, AuditEntry{At: time.Unix(int64(i), 0), Agent: "a", Method: "RunAction", Phase: AuditIntent, Detail: fmt.Sprint(i)}))
	}
	all, err := s.ListAudit(ctx, AuditFilter{Limit: 100})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(all), 10+5)
	assert.Equal(t, "39", all[0].Detail, "the newest kept")
}

func TestAuditDetailIsBounded(t *testing.T) {
	ctx := context.Background()
	s := open(t, filepath.Join(t.TempDir(), "db"))
	long := make([]byte, 2000)
	for i := range long {
		long[i] = 'x'
	}
	require.NoError(t, s.AppendAudit(ctx, AuditEntry{At: time.Now(), Agent: "a", Method: "RunEdit", Phase: AuditOutcome, Detail: string(long)}))
	all, _ := s.ListAudit(ctx, AuditFilter{Limit: 1})
	assert.LessOrEqual(t, len([]rune(all[0].Detail)), 500)
}

// Migration 0003 on a database of P13 (0002): the old data stays.
func TestAgentMigrationOnAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s := open(t, path)
	require.NoError(t, s.TouchRecent(ctx, rec("a", "web", "1", 1), 50, 500))
	require.NoError(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{`DROP TABLE agent_grants`, `DROP TABLE agent_targets`, `DROP TABLE agent_audit`, `DELETE FROM schema_migrations WHERE version >= 3`} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, s.Close())
	s = open(t, path)
	got, err := s.RecentObjects(ctx, "k", "a")
	require.NoError(t, err)
	assert.Len(t, got, 1)
	targets, err := s.AgentTargets(ctx)
	require.NoError(t, err)
	assert.Empty(t, targets)
}

func TestGroupNamesAndSwitchesSurviveReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	require.NoError(t, err)
	scope := agentgrant.Scope{Mode: agentgrant.ScopeOne, Name: "a"}
	target := agentgrant.Target{Provider: "k", Target: "t", Identity: "i", Title: "Target",
		DisabledScopes: []agentgrant.Scope{scope},
		Groups: []agentgrant.Group{
			{ID: "g1", Name: "Диагностика", Scope: scope, Grants: []agentgrant.Grant{grantOne("a", "read")}},
			{ID: "g2", Name: "Maintenance", Scope: scope, Disabled: true, Grants: []agentgrant.Grant{grantOne("a", "edit", "configmaps")}},
			{ID: "empty", Name: "Draft", Scope: scope},
		}}
	require.NoError(t, s.ReplaceAgentGrants(ctx, target))
	require.NoError(t, s.Close())
	s, err = Open(ctx, path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	got, err := s.AgentTargets(ctx)
	require.NoError(t, err)
	require.Equal(t, []agentgrant.Target{target}, got)
	assert.Empty(t, got[0].EffectiveGrants())
	target.DisabledScopes = nil
	target.Groups[1].Disabled = false
	require.NoError(t, s.ReplaceAgentGrants(ctx, target))
	got, err = s.AgentTargets(ctx)
	require.NoError(t, err)
	assert.Len(t, got[0].EffectiveGrants(), 2)
	// A scope with no groups can still be paused, including inherited All rights.
	target.Groups = nil
	target.DisabledScopes = []agentgrant.Scope{scope}
	require.NoError(t, s.ReplaceAgentGrants(ctx, target))
	got, err = s.AgentTargets(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, target.DisabledScopes, got[0].DisabledScopes)
}

func TestGroupMigrationPreservesLegacyGrants(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(ctx, path)
	require.NoError(t, err)
	original := agentgrant.Target{Provider: "k", Target: "t", Identity: "i", Grants: []agentgrant.Grant{grantOne("a", "read"), grantOne("a", "read", "pods")}}
	require.NoError(t, s.ReplaceAgentGrants(ctx, original))
	require.NoError(t, s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{"ALTER TABLE agent_targets DROP COLUMN groups_json", "ALTER TABLE agent_targets DROP COLUMN disabled_scopes_json", "DELETE FROM schema_migrations WHERE version=4"} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}))
	require.NoError(t, s.Close())
	s, err = Open(ctx, path)
	require.NoError(t, err)
	defer func() { require.NoError(t, s.Close()) }()
	got, err := s.AgentTargets(ctx)
	require.NoError(t, err)
	assert.Equal(t, []agentgrant.Target{original}, got)
}
