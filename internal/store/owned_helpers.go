package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/spk/spk-ocular/internal/core"
)

type HelperRecord struct {
	Helper                     core.OwnedHelper
	Owner                      string
	LeaseUntil, NextTry, Delay int64
}

func (s *Store) InsertHelper(ctx context.Context, r HelperRecord) error {
	data, err := json.Marshal(r.Helper)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO owned_helpers(id,provider,target,identity_json,uid,owner,lease_until,next_try,delay_ms) VALUES(?,?,?,?,?,?,?,?,?)`, r.Helper.ID, r.Helper.Provider, r.Helper.Target, string(data), r.Helper.UID, r.Owner, r.LeaseUntil, r.NextTry, r.Delay)
	return err
}
func (s *Store) DueHelpers(ctx context.Context, provider, target string, now int64) ([]HelperRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT identity_json,uid,owner,lease_until,next_try,delay_ms FROM owned_helpers WHERE provider=? AND target=? AND next_try<=? AND lease_until<=? ORDER BY next_try,id LIMIT 32`, provider, target, now, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HelperRecord
	for rows.Next() {
		var r HelperRecord
		var data, uid string
		if err = rows.Scan(&data, &uid, &r.Owner, &r.LeaseUntil, &r.NextTry, &r.Delay); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(data), &r.Helper); err != nil {
			return nil, err
		}
		r.Helper.UID = uid
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) ClaimHelper(ctx context.Context, id, owner string, now, until int64) (bool, error) {
	r, err := s.db.ExecContext(ctx, `UPDATE owned_helpers SET owner=?,lease_until=? WHERE id=? AND lease_until<=? AND next_try<=?`, owner, until, id, now, now)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) RenewHelper(ctx context.Context, id, owner string, until int64) error {
	r, err := s.db.ExecContext(ctx, `UPDATE owned_helpers SET lease_until=? WHERE id=? AND owner=?`, until, id, owner)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("helper ownership lease changed")
	}
	return err
}
func (s *Store) PinHelperUID(ctx context.Context, id, owner, uid string) error {
	r, err := s.db.ExecContext(ctx, `UPDATE owned_helpers SET uid=? WHERE id=? AND owner=? AND (uid='' OR uid=?)`, uid, id, owner, uid)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("helper UID or ownership lease changed")
	}
	return err
}
func (s *Store) FinishHelper(ctx context.Context, id, owner string, clean bool, now int64) error {
	if clean {
		_, err := s.db.ExecContext(ctx, `DELETE FROM owned_helpers WHERE id=? AND owner=?`, id, owner)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE owned_helpers SET next_try=?+delay_ms, delay_ms=MIN(delay_ms*2,86400000),owner='',lease_until=0 WHERE id=? AND owner=?`, now, id, owner)
	return err
}

// HelperIntents exposes bounded non-secret identities for visible recovery status.
// It includes active leases: callers distinguish them from active in-memory runs.
func (s *Store) HelperIntents(ctx context.Context) ([]core.OwnedHelper, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT identity_json,uid FROM owned_helpers ORDER BY id LIMIT 256`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.OwnedHelper{}
	for rows.Next() {
		var data, uid string
		if err = rows.Scan(&data, &uid); err != nil {
			return nil, err
		}
		var h core.OwnedHelper
		if err = json.Unmarshal([]byte(data), &h); err != nil {
			return nil, err
		}
		h.UID = uid
		out = append(out, h)
	}
	return out, rows.Err()
}
