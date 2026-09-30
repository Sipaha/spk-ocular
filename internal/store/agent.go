package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/spk/spk-ocular/internal/agentgrant"
)

// AgentTargets lists the targets with grants, by provider and target.
func (s *Store) AgentTargets(ctx context.Context) ([]agentgrant.Target, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT provider, target, title, identity, observed FROM agent_targets ORDER BY provider, target`)
	if err != nil {
		return nil, err
	}
	var out []agentgrant.Target
	for rows.Next() {
		var t agentgrant.Target
		if err := rows.Scan(&t.Provider, &t.Target, &t.Title, &t.Identity, &t.Observed); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, t)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants, err := s.db.QueryContext(ctx, `SELECT provider, target, scope_mode, scope_name, verb, kinds, no_confirm FROM agent_grants ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = grants.Close() }()
	idx := map[[2]string]int{}
	for i, t := range out {
		idx[[2]string{t.Provider, t.Target}] = i
	}
	for grants.Next() {
		var (
			p, tg, mode, name, verb string
			kinds                   sql.NullString
			noConfirm               bool
		)
		if err := grants.Scan(&p, &tg, &mode, &name, &verb, &kinds, &noConfirm); err != nil {
			return nil, err
		}
		g := agentgrant.Grant{Scope: agentgrant.Scope{Mode: agentgrant.ScopeMode(mode), Name: name}, Verb: verb, NoConfirm: noConfirm}
		if kinds.Valid {
			if err := json.Unmarshal([]byte(kinds.String), &g.Kinds); err != nil {
				return nil, fmt.Errorf("grant kinds: %w", err)
			}
		}
		if i, ok := idx[[2]string{p, tg}]; ok {
			out[i].Grants = append(out[i].Grants, g)
		}
	}
	return out, grants.Err()
}

// ReplaceAgentGrants sets t's grants whole (none: the target goes). A new
// target is recorded with t.Identity; an existing one keeps the identity it
// was granted for (ReconfirmAgentTarget moves it) and takes t.Title.
func (s *Store) ReplaceAgentGrants(ctx context.Context, t agentgrant.Target) error {
	for _, g := range t.Grants {
		if err := g.Validate(); err != nil {
			return err
		}
	}
	if len(t.Grants) > 0 && t.Identity == "" {
		return errors.New("a target's grants need its identity")
	}
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if len(t.Grants) == 0 {
			_, err := tx.ExecContext(ctx, `DELETE FROM agent_targets WHERE provider = ? AND target = ?`, t.Provider, t.Target)
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO agent_targets(provider, target, title, identity, updated_at) VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(provider, target) DO UPDATE SET title = excluded.title, updated_at = excluded.updated_at`,
			t.Provider, t.Target, t.Title, t.Identity, time.Now().UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM agent_grants WHERE provider = ? AND target = ?`, t.Provider, t.Target); err != nil {
			return err
		}
		for _, g := range t.Grants {
			var kinds any
			if g.Kinds != nil {
				b, _ := json.Marshal(g.Kinds)
				kinds = string(b)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO agent_grants(provider, target, scope_mode, scope_name, verb, kinds, no_confirm) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				t.Provider, t.Target, string(g.Scope.Mode), g.Scope.Name, g.Verb, kinds, g.NoConfirm); err != nil {
				return err
			}
		}
		return nil
	})
}

// RevokeAllAgentGrants removes every grant.
func (s *Store) RevokeAllAgentGrants(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM agent_targets`)
	return err
}

// ObserveAgentIdentity records the identity a call saw for a target with
// grants: another than granted suspends them (observed), the granted one
// resumes them. changed: the target's state changed by it.
func (s *Store) ObserveAgentIdentity(ctx context.Context, provider, target, identity string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_targets SET observed = CASE WHEN identity = ?1 THEN '' ELSE ?1 END, updated_at = ?4
		 WHERE provider = ?2 AND target = ?3 AND observed <> CASE WHEN identity = ?1 THEN '' ELSE ?1 END`,
		identity, provider, target, time.Now().UnixMilli())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ReconfirmAgentTarget grants the target's grants for the identity
// observed, if it is still the one the user was shown (false: it changed
// again, or the target is not suspended).
func (s *Store) ReconfirmAgentTarget(ctx context.Context, provider, target, observed string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agent_targets SET identity = observed, observed = '', updated_at = ? WHERE provider = ? AND target = ? AND observed <> '' AND observed = ?`,
		time.Now().UnixMilli(), provider, target, observed)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Audit phases.
const (
	// AuditRead: a read, folded per minute, agent, method and target.
	AuditRead = "read"
	// AuditIntent: a write about to be sent.
	AuditIntent = "intent"
	// AuditOutcome: what became of a write (or a pending plan).
	AuditOutcome = "outcome"
	// AuditRefused: a call refused before reaching the target.
	AuditRefused = "refused"
)

// AuditSeveral is the scope or object of a folded read of several.
const AuditSeveral = "*"

// AuditEntry is one journal record.
type AuditEntry struct {
	ID          int64     `json:"id"`
	At          time.Time `json:"at"`
	Agent       string    `json:"agent"`
	Method      string    `json:"method"`
	Provider    string    `json:"provider,omitempty"`
	Target      string    `json:"target,omitempty"`
	Scope       string    `json:"scope,omitempty"`
	Object      string    `json:"object,omitempty"`
	Verb        string    `json:"verb,omitempty"`
	Destructive bool      `json:"destructive,omitempty"`
	ExpectHash  string    `json:"expectHash,omitempty"`
	Phase       string    `json:"phase"`
	Outcome     string    `json:"outcome,omitempty"`
	Detail      string    `json:"detail,omitempty"`
	// Count: the reads folded into a read record.
	Count int `json:"count"`
}

// The journal keeps the newest auditKeep records, trimmed every
// auditRotateEvery insertions (vars for tests).
var (
	auditKeep        int64 = 20000
	auditRotateEvery int64 = 100
)

// maxAuditDetail bounds a record's detail (characters).
const maxAuditDetail = 500

// AppendAudit records e; a read joins its minute's record (its time is the
// first read's; its scope and object AuditSeveral unless every read named
// the same).
func (s *Store) AppendAudit(ctx context.Context, e AuditEntry) error {
	if r := []rune(e.Detail); len(r) > maxAuditDetail {
		e.Detail = string(r[:maxAuditDetail-1]) + "…"
	}
	var bucket any
	if e.Phase == AuditRead {
		bucket = fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s", e.At.Unix()/60, e.Agent, e.Method, e.Provider, e.Target)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_audit(at, agent, method, provider, target, scope, object, verb, destructive, expect_hash, phase, outcome, detail, bucket)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(bucket) WHERE bucket IS NOT NULL DO UPDATE SET count = count + 1, outcome = excluded.outcome, detail = excluded.detail,
		   scope = CASE WHEN scope = excluded.scope THEN scope ELSE '*' END,
		   object = CASE WHEN object = excluded.object THEN object ELSE '*' END`,
		e.At.UnixMilli(), e.Agent, e.Method, e.Provider, e.Target, e.Scope, e.Object, e.Verb, e.Destructive, e.ExpectHash, e.Phase, e.Outcome, e.Detail, bucket)
	if err != nil {
		return err
	}
	if id, err := res.LastInsertId(); err == nil && id%auditRotateEvery == 0 {
		_, err = s.db.ExecContext(ctx, `DELETE FROM agent_audit WHERE id <= ?`, id-auditKeep)
		return err
	}
	return nil
}

// AuditFilter narrows ListAudit; Before is a record id (the page's last).
type AuditFilter struct {
	Agent    string `json:"agent,omitempty"`
	Provider string `json:"provider,omitempty"`
	Target   string `json:"target,omitempty"`
	Before   int64  `json:"before,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ListAudit lists records newest first.
func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]AuditEntry, error) {
	q := `SELECT id, at, agent, method, provider, target, scope, object, verb, destructive, expect_hash, phase, outcome, detail, count FROM agent_audit WHERE 1 = 1`
	var args []any
	if f.Agent != "" {
		q += ` AND agent = ?`
		args = append(args, f.Agent)
	}
	if f.Provider != "" {
		q += ` AND provider = ?`
		args = append(args, f.Provider)
	}
	if f.Target != "" {
		q += ` AND target = ?`
		args = append(args, f.Target)
	}
	if f.Before > 0 {
		q += ` AND id < ?`
		args = append(args, f.Before)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, max(1, f.Limit))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []AuditEntry
	for rows.Next() {
		var (
			e  AuditEntry
			at int64
		)
		if err := rows.Scan(&e.ID, &at, &e.Agent, &e.Method, &e.Provider, &e.Target, &e.Scope, &e.Object, &e.Verb, &e.Destructive, &e.ExpectHash, &e.Phase, &e.Outcome, &e.Detail, &e.Count); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		out = append(out, e)
	}
	return out, rows.Err()
}
