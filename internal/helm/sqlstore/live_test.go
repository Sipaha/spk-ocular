package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/release"
	common "helm.sh/helm/v4/pkg/release/common"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	upstream "helm.sh/helm/v4/pkg/storage/driver"
)

// A real server is opt-in and must carry the runner's disposable database marker.
func liveDatabase(t *testing.T) (*sql.DB, string) {
	t.Helper()
	if os.Getenv("OCULAR_HELM_LIVE") != "1" {
		t.Skip("run make test-helm-live")
	}
	raw, err := os.ReadFile(os.Getenv("OCULAR_HELM_SQL_DSN_FILE"))
	require.NoError(t, err)
	u, err := url.Parse(strings.TrimSpace(string(raw)))
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", u.Hostname())
	require.Equal(t, "/ocular_helm_live", u.Path)
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	var marker string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT shobj_description(oid, 'pg_database') FROM pg_database WHERE datname = current_database()").Scan(&marker))
	require.Equal(t, "ocular disposable Helm integration fixture", marker)
	schema := fmt.Sprintf("ocular_%d", time.Now().UnixNano())
	_, err = db.ExecContext(t.Context(), "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return db, u.String()
}

func liveRelease(namespace string) *releasev1.Release {
	return &releasev1.Release{Name: "demo", Namespace: namespace, Version: 1,
		Info:   &releasev1.Info{Status: common.StatusDeployed},
		Chart:  &chartv2.Chart{Metadata: &chartv2.Metadata{APIVersion: "v2", Name: "fixture", Version: "1.0.0"}},
		Config: map[string]any{"message": "first"}, Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: demo\n",
		Labels: map[string]string{"team": "integration"}}
}

func TestSQLLiveUpstreamCompatibilityAndIsolation(t *testing.T) {
	admin, dsn := liveDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := NewSQL(ctx, dsn, "blue")
	require.NoError(t, err)
	var migrations int
	require.NoError(t, d.db.GetContext(ctx, &migrations, "SELECT count(*) FROM gorp_migrations"))
	require.Equal(t, 2, migrations)
	key := "sh.helm.release.v1.demo.v1"
	r := liveRelease("blue")
	require.NoError(t, d.Create(key, r))
	require.ErrorIs(t, d.Create(key, r), upstream.ErrReleaseExists)
	// The unmodified upstream Helm driver must read and update our schema/codec.
	// It has no Close method; terminate its identified test socket at cleanup.
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	app := fmt.Sprintf("ocular_upstream_%d", time.Now().UnixNano())
	q.Set("application_name", app)
	u.RawQuery = q.Encode()
	reference, err := upstream.NewSQL(u.String(), "blue")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_, _ = admin.ExecContext(cleanup, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name=$1", app)
	})
	raw, err := reference.Get(key)
	require.NoError(t, err)
	got := raw.(*releasev1.Release)
	require.Equal(t, "integration", got.Labels["team"])
	require.Equal(t, r.Manifest, got.Manifest)
	got.Config["message"] = "updated by upstream"
	require.NoError(t, reference.Update(key, got))
	raw, err = d.Get(key)
	require.NoError(t, err)
	require.Equal(t, "updated by upstream", raw.(*releasev1.Release).Config["message"])
	green, err := NewSQL(ctx, dsn, "green")
	require.NoError(t, err)
	require.NoError(t, green.Create(key, liveRelease("green")))
	rows, err := d.List(func(release.Releaser) bool { return true })
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "blue", rows[0].(*releasev1.Release).Namespace)
	_, err = reference.Query(map[string]string{"team": "integration"})
	require.ErrorContains(t, err, "unknown label team", "upstream SQL queries accept system labels only")
	_, err = d.Query(map[string]string{"team": "integration"})
	require.ErrorContains(t, err, "unknown label team")
	rows, err = d.Query(map[string]string{"name": "demo", "owner": "helm"})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	_, err = d.Delete(key)
	require.NoError(t, err)
	_, err = d.Get(key)
	require.ErrorIs(t, err, upstream.ErrReleaseNotFound)
	_, err = green.Get(key)
	require.NoError(t, err, "deleting blue must not delete same-named green release")
	t.Log("upstream schema/codec interoperability, migration reuse, labels and namespace isolation verified")
}

func TestSQLLiveCancellationClosesConnections(t *testing.T) {
	_, dsn := liveDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := NewSQL(ctx, dsn, "blue")
	require.NoError(t, err)
	require.NoError(t, d.Create("key", liveRelease("blue")))
	locker, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer func() { _ = locker.Close() }()
	tx, err := locker.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(t.Context(), "LOCK TABLE releases_v1 IN ACCESS EXCLUSIVE MODE")
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := d.Get("key"); done <- err }()
	require.Eventually(t, func() bool { return d.db.Stats().InUse == 1 }, 5*time.Second, 10*time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("blocked SQL read ignored cancellation")
	}
	require.Eventually(t, func() bool { return d.db.Stats().OpenConnections == 0 }, 3*time.Second, 10*time.Millisecond)
	t.Logf("blocked real query cancelled and pool closed in %s", time.Since(started))
}

func TestSQLLivePermissionFailureAndRLS(t *testing.T) {
	admin, dsn := liveDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := NewSQL(ctx, dsn, "blue")
	require.NoError(t, err)
	require.NoError(t, d.Create("key", liveRelease("blue")))
	role := fmt.Sprintf("ocular_reader_%d", time.Now().UnixNano())
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	schema := u.Query().Get("search_path")
	_, err = admin.ExecContext(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD 'disposable'; GRANT USAGE ON SCHEMA "+schema+" TO "+role)
	require.NoError(t, err)
	_, err = d.db.ExecContext(ctx, "GRANT SELECT ON gorp_migrations TO "+role+"; CREATE POLICY read_blue ON releases_v1 FOR SELECT TO "+role+" USING (namespace = 'blue'); CREATE POLICY read_blue_labels ON custom_labels_v1 FOR SELECT TO "+role+" USING (releaseNamespace = 'blue')")
	require.NoError(t, err)
	u.User = url.UserPassword(role, "disposable")
	restricted, err := NewSQL(ctx, u.String(), "blue")
	require.NoError(t, err)
	_, err = restricted.Get("key")
	require.NoError(t, err)
	_, err = d.db.ExecContext(ctx, "REVOKE SELECT ON releases_v1 FROM PUBLIC")
	require.NoError(t, err)
	_, err = restricted.Get("key")
	require.Error(t, err)
	require.NotErrorIs(t, err, upstream.ErrReleaseNotFound)
	require.ErrorContains(t, err, "permission denied")
	t.Log("existing migrations work without DDL rights; RLS read allowed and revoked SELECT reported as denial")
}

func TestSQLLiveCommitFailureIsAtomic(t *testing.T) {
	_, dsn := liveDatabase(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := NewSQL(ctx, dsn, "blue")
	require.NoError(t, err)
	_, err = d.db.ExecContext(ctx, `CREATE FUNCTION reject_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture commit rejected'; END $$;
 CREATE CONSTRAINT TRIGGER reject_commit AFTER INSERT ON releases_v1 DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION reject_commit()`)
	require.NoError(t, err)
	err = d.Create("key", liveRelease("blue"))
	require.ErrorContains(t, err, "fixture commit rejected")
	var count int
	require.NoError(t, d.db.GetContext(ctx, &count, "SELECT count(*) FROM releases_v1"))
	require.Zero(t, count)
	require.NoError(t, d.db.GetContext(ctx, &count, "SELECT count(*) FROM custom_labels_v1"))
	require.Zero(t, count)
	t.Log("deferred commit failure propagated; neither release nor custom labels persisted")
}

func TestSQLLiveConnectionLossRollsBack(t *testing.T) {
	admin, dsn := liveDatabase(t)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	app := fmt.Sprintf("ocular_loss_%d", time.Now().UnixNano())
	q.Set("application_name", app)
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := NewSQL(ctx, u.String(), "blue")
	require.NoError(t, err)
	_, err = d.db.ExecContext(ctx, `CREATE FUNCTION block_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(30); RETURN NEW; END $$;
 CREATE TRIGGER block_insert BEFORE INSERT ON releases_v1 FOR EACH ROW EXECUTE FUNCTION block_insert()`)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { done <- d.Create("key", liveRelease("blue")) }()
	var pid int
	require.Eventually(t, func() bool {
		return admin.QueryRowContext(t.Context(), "SELECT pid FROM pg_stat_activity WHERE application_name=$1 AND wait_event='PgSleep'", app).Scan(&pid) == nil
	}, 5*time.Second, 20*time.Millisecond)
	var stopped bool
	require.NoError(t, admin.QueryRowContext(t.Context(), "SELECT pg_terminate_backend($1)", pid).Scan(&stopped))
	require.True(t, stopped)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("SQL create did not return after backend termination")
	}
	var count int
	require.NoError(t, d.db.GetContext(t.Context(), &count, "SELECT count(*) FROM releases_v1"))
	require.Zero(t, count)
	t.Log("real backend connection loss reported without replay; insert transaction rolled back")
}
