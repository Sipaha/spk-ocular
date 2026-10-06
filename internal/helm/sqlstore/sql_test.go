package sqlstore

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	sq "github.com/Masterminds/squirrel"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	common "helm.sh/helm/v4/pkg/release/common"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
)

func fixture(t *testing.T) (*SQL, sqlmock.Sqlmock) {
	t.Helper()
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return &SQL{db: sqlx.NewDb(db, "sqlmock"), ctx: t.Context(), namespace: "blue", logger: slog.New(slog.NewTextHandler(io.Discard, nil)), statementBuilder: sq.StatementBuilder.PlaceholderFormat(sq.Dollar)}, m
}
func TestSQLClosesPoolWhenOperationEnds(t *testing.T) {
	db, m, err := sqlmock.New()
	require.NoError(t, err)
	// Initialization retains one idle socket only until the operation ends.
	m.ExpectQuery(regexp.QuoteMeta("SELECT id, applied_at FROM gorp_migrations ORDER BY id")).WillReturnRows(sqlmock.NewRows([]string{"id", "applied_at"}).AddRow("init", time.Now()).AddRow("custom_labels", time.Now()))
	m.ExpectClose()
	ctx, cancel := context.WithCancel(t.Context())
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	_, err = withDB(ctx, sqlxDB, "blue")
	require.NoError(t, err)
	cancel()
	require.Eventually(t, func() bool { return m.ExpectationsWereMet() == nil }, time.Second, time.Millisecond)
	require.Eventually(t, func() bool { return sqlxDB.PingContext(context.Background()) != nil }, time.Second, time.Millisecond)
}
func TestSQLReadFailureIsNotAbsence(t *testing.T) {
	d, m := fixture(t)
	m.ExpectQuery("SELECT body FROM releases_v1").WithArgs("sh.helm.release.v1.demo.v1", "blue").WillReturnError(errors.New("permission denied"))
	_, err := d.Get("sh.helm.release.v1.demo.v1")
	require.ErrorContains(t, err, "permission denied")
	require.NotErrorIs(t, err, driver.ErrReleaseNotFound)
	require.NoError(t, m.ExpectationsWereMet())
}
func TestSQLCreateReportsCommitFailure(t *testing.T) {
	d, m := fixture(t)
	m.ExpectBegin()
	m.ExpectExec("INSERT INTO releases_v1").WillReturnResult(sqlmock.NewResult(0, 1))
	m.ExpectCommit().WillReturnError(errors.New("commit connection lost"))
	err := d.Create("sh.helm.release.v1.demo.v1", &release.Release{Name: "demo", Namespace: "blue", Version: 1, Info: &release.Info{Status: common.StatusDeployed}})
	require.ErrorContains(t, err, "commit connection lost")
	require.NoError(t, m.ExpectationsWereMet())
}
func TestSQLQueryCancelled(t *testing.T) {
	d, m := fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	d.ctx = ctx
	m.ExpectQuery("SELECT body FROM releases_v1").WithArgs("key", "blue").WillDelayFor(time.Second).WillReturnRows(sqlmock.NewRows([]string{"body"}))
	started := time.Now()
	_, err := d.Get("key")
	require.Error(t, err)
	require.Less(t, time.Since(started), 500*time.Millisecond)
	require.NoError(t, m.ExpectationsWereMet())
}

func TestSQLDeleteCommitsBothReleaseAndLabels(t *testing.T) {
	for _, commitErr := range []error{nil, errors.New("delete commit lost")} {
		name := "success"
		if commitErr != nil {
			name = "commit failure"
		}
		t.Run(name, func(t *testing.T) {
			d, m := fixture(t)
			body, err := encodeRelease(&release.Release{Name: "demo", Namespace: "blue", Version: 1, Info: &release.Info{Status: common.StatusDeployed}})
			require.NoError(t, err)
			m.ExpectBegin()
			m.ExpectQuery("SELECT body FROM releases_v1").WithArgs("key", "blue").WillReturnRows(sqlmock.NewRows([]string{"body"}).AddRow(body))
			m.ExpectExec("DELETE FROM releases_v1").WithArgs("key", "blue").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectQuery("SELECT key, value FROM custom_labels_v1").WithArgs("key", "blue").WillReturnRows(sqlmock.NewRows([]string{"key", "value"}).AddRow("team", "test"))
			m.ExpectExec("DELETE FROM custom_labels_v1").WithArgs("key", "blue").WillReturnResult(sqlmock.NewResult(0, 1))
			m.ExpectCommit().WillReturnError(commitErr)
			deleted, err := d.Delete("key")
			if commitErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, commitErr)
			}
			require.Equal(t, "demo", deleted.(*release.Release).Name)
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
