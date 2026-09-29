package store

import (
	"context"
	"database/sql"
	"errors"
)

// GetUIPref returns a stored app-wide UI preference, "" if it was never set.
func (s *Store) GetUIPref(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM ui_prefs WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetUIPref upserts one app-wide UI preference.
func (s *Store) SetUIPref(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ui_prefs(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

// DeleteUIPref removes one preference; a missing key is not an error.
func (s *Store) DeleteUIPref(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ui_prefs WHERE key = ?`, key)
	return err
}

// GetTargetState returns one per-target value, "" if it was never set.
func (s *Store) GetTargetState(ctx context.Context, provider, target, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM target_state WHERE provider = ? AND target = ? AND key = ?`,
		provider, target, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetTargetState upserts one per-target value.
func (s *Store) SetTargetState(ctx context.Context, provider, target, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO target_state(provider, target, key, value) VALUES (?, ?, ?, ?)
		 ON CONFLICT(provider, target, key) DO UPDATE SET value = excluded.value`,
		provider, target, key, value)
	return err
}

// TargetState returns all per-target values.
func (s *Store) TargetState(ctx context.Context, provider, target string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM target_state WHERE provider = ? AND target = ?`, provider, target)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
