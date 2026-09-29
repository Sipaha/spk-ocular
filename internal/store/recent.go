package store

import (
	"context"
	"database/sql"
	"time"
)

// Recent is one object whose details were opened.
type Recent struct {
	Provider, Target, Kind, Scope, Name, UID string
	Title                                    string
	OpenedAt                                 time.Time
}

// TouchRecent records r as opened now (again: it moves up) and drops the
// oldest entries beyond perTarget for r's target and beyond total overall.
func (s *Store) TouchRecent(ctx context.Context, r Recent, perTarget, total int) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO recent_objects(provider, target, kind, scope, name, uid, title, opened_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(provider, target, kind, scope, name, uid) DO UPDATE SET title = excluded.title, opened_at = excluded.opened_at`,
			r.Provider, r.Target, r.Kind, r.Scope, r.Name, r.UID, r.Title, r.OpenedAt.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM recent_objects WHERE provider = ? AND target = ? AND rowid NOT IN (
			   SELECT rowid FROM recent_objects WHERE provider = ? AND target = ? ORDER BY opened_at DESC, rowid DESC LIMIT ?)`,
			r.Provider, r.Target, r.Provider, r.Target, perTarget); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`DELETE FROM recent_objects WHERE rowid NOT IN (SELECT rowid FROM recent_objects ORDER BY opened_at DESC, rowid DESC LIMIT ?)`, total)
		return err
	})
}

// RecentObjects lists a target's recent objects, newest first.
func (s *Store) RecentObjects(ctx context.Context, provider, target string) ([]Recent, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, scope, name, uid, title, opened_at FROM recent_objects WHERE provider = ? AND target = ? ORDER BY opened_at DESC, rowid DESC`,
		provider, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recent
	for rows.Next() {
		r := Recent{Provider: provider, Target: target}
		var at int64
		if err := rows.Scan(&r.Kind, &r.Scope, &r.Name, &r.UID, &r.Title, &at); err != nil {
			return nil, err
		}
		r.OpenedAt = time.UnixMilli(at)
		out = append(out, r)
	}
	return out, rows.Err()
}
