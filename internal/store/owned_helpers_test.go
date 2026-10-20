package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/spk/spk-ocular/internal/core"
	"github.com/stretchr/testify/require"
)

func TestHelperRegistryPersistsUIDLeasesAndExponentialSchedule(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	s := open(t, path)
	h := core.OwnedHelper{ID: "nonce", Provider: "k", Target: "a", Namespace: "tools", NamespaceUID: "ns-uid", Name: "ocular-node-shell-test", NodeName: "node", NodeUID: "node-uid", CreatedAt: 1000}
	require.NoError(t, s.InsertHelper(ctx, HelperRecord{Helper: h, Owner: "live-owner", LeaseUntil: 2000, NextTry: 1000, Delay: time.Minute.Milliseconds()}))
	rows, err := s.DueHelpers(ctx, "k", "a", 1999)
	require.NoError(t, err)
	require.Empty(t, rows)
	claimed, err := s.ClaimHelper(ctx, h.ID, "other", 1999, 3000)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NoError(t, s.PinHelperUID(ctx, h.ID, "live-owner", "pod-uid"))
	require.Error(t, s.PinHelperUID(ctx, h.ID, "live-owner", "replacement"))
	require.Error(t, s.RenewHelper(ctx, h.ID, "foreign", 3000))
	require.NoError(t, s.FinishHelper(ctx, h.ID, "live-owner", false, 2000))
	require.NoError(t, s.Close())
	s = open(t, path)
	now := int64(2000)
	delay := time.Minute.Milliseconds()
	for range 16 {
		rows, err = s.DueHelpers(ctx, "k", "a", now+24*time.Hour.Milliseconds())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		r := rows[0]
		require.Equal(t, "pod-uid", r.Helper.UID)
		require.Equal(t, now+delay, r.NextTry)
		require.Equal(t, h.NamespaceUID, r.Helper.NamespaceUID)
		early, err := s.DueHelpers(ctx, "k", "a", r.NextTry-1)
		require.NoError(t, err)
		require.Empty(t, early)
		now = r.NextTry
		claimed, err = s.ClaimHelper(ctx, h.ID, "recovery", now, now+10000)
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = s.ClaimHelper(ctx, h.ID, "racer", now, now+10000)
		require.NoError(t, err)
		require.False(t, claimed)
		require.NoError(t, s.FinishHelper(ctx, h.ID, "recovery", false, now))
		delay = min(delay*2, 24*time.Hour.Milliseconds())
	}
	require.Equal(t, 24*time.Hour.Milliseconds(), delay)
	rows, err = s.DueHelpers(ctx, "k", "a", now+24*time.Hour.Milliseconds())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	claimed, err = s.ClaimHelper(ctx, h.ID, "last", rows[0].NextTry, rows[0].NextTry+10000)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NoError(t, s.FinishHelper(ctx, h.ID, "last", true, rows[0].NextTry))
	rows, err = s.DueHelpers(ctx, "k", "a", now+48*time.Hour.Milliseconds())
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestHelperMigrationPreservesExistingVersionFourPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	// Reconstruct the actual previous schema, including its migration receipts.
	db, err := sql.Open("sqlite", "file:"+path)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY)`)
	require.NoError(t, err)
	for i, name := range []string{"0001_init.sql", "0002_recent_objects.sql", "0003_agent_access.sql", "0004_agent_groups.sql"} {
		data, err := migrationsFS.ReadFile("migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(string(data))
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO schema_migrations(version) VALUES(?)`, i+1)
		require.NoError(t, err)
	}
	_, err = db.Exec(`INSERT INTO ui_prefs(key,value) VALUES('legacy','preserved')`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	s := open(t, path)
	value, err := s.GetUIPref(t.Context(), "legacy")
	require.NoError(t, err)
	require.Equal(t, "preserved", value)
	require.NoError(t, s.InsertHelper(t.Context(), HelperRecord{Helper: core.OwnedHelper{ID: "new", Provider: "k", Target: "a"}, Owner: "lease", Delay: 60000}))
}
