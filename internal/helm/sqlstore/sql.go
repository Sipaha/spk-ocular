/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"sort"
	"strconv"
	"time"

	"github.com/jmoiron/sqlx"
	migrate "github.com/rubenv/sql-migrate"

	sq "github.com/Masterminds/squirrel"

	// Import pq for postgres dialect
	"github.com/lib/pq"

	"helm.sh/helm/v4/pkg/release"
	rspb "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
)

var _ driver.Driver = (*SQL)(nil)

var labelMap = map[string]struct{}{
	"modifiedAt": {},
	"createdAt":  {},
	"version":    {},
	"status":     {},
	"owner":      {},
	"name":       {},
}

const postgreSQLDialect = "postgres"

// SQLDriverName is the string name of this driver.
const SQLDriverName = "SQL"

const (
	sqlReleaseTableName      = "releases_v1"
	sqlCustomLabelsTableName = "custom_labels_v1"
)

const (
	sqlReleaseTableKeyColumn        = "key"
	sqlReleaseTableTypeColumn       = "type"
	sqlReleaseTableBodyColumn       = "body"
	sqlReleaseTableNameColumn       = "name"
	sqlReleaseTableNamespaceColumn  = "namespace"
	sqlReleaseTableVersionColumn    = "version"
	sqlReleaseTableStatusColumn     = "status"
	sqlReleaseTableOwnerColumn      = "owner"
	sqlReleaseTableCreatedAtColumn  = "createdAt"
	sqlReleaseTableModifiedAtColumn = "modifiedAt"

	sqlCustomLabelsTableReleaseKeyColumn       = "releaseKey"
	sqlCustomLabelsTableReleaseNamespaceColumn = "releaseNamespace"
	sqlCustomLabelsTableKeyColumn              = "key"
	sqlCustomLabelsTableValueColumn            = "value"
)

// Following limits based on k8s labels limits - https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
const (
	sqlCustomLabelsTableKeyMaxLength   = 253 + 1 + 63
	sqlCustomLabelsTableValueMaxLength = 63
)

const (
	sqlReleaseDefaultOwner = "helm"
	sqlReleaseDefaultType  = "helm.sh/release.v1"
)

// SQL is the sql storage driver implementation.
type SQL struct {
	db               *sqlx.DB
	namespace        string
	statementBuilder sq.StatementBuilderType
	// Embed a LogHolder to provide logger functionality
	ctx    context.Context
	logger *slog.Logger
}

// Name returns the name of the driver.
func (s *SQL) Name() string {
	return SQLDriverName
}

// Check if all migrations al
func (s *SQL) checkAlreadyApplied(migrations []*migrate.Migration) bool {
	// make map (set) of ids for fast search
	migrationsIDs := make(map[string]struct{})
	for _, migration := range migrations {
		migrationsIDs[migration.Id] = struct{}{}
	}

	// get list of applied migrations
	records := []*migrate.MigrationRecord{}
	err := s.db.SelectContext(s.ctx, &records, "SELECT id, applied_at FROM gorp_migrations ORDER BY id")
	if err != nil {
		s.Logger().Debug("failed to get migration records", slog.Any("error", err))
		return false
	}

	for _, record := range records {
		if _, ok := migrationsIDs[record.Id]; ok {
			s.Logger().Debug("found previous migration", "id", record.Id, "appliedAt", record.AppliedAt)
			delete(migrationsIDs, record.Id)
		}
	}

	// check if all migrations applied
	if len(migrationsIDs) != 0 {
		for id := range migrationsIDs {
			s.Logger().Debug("find unapplied migration", "id", id)
		}
		return false
	}
	return true
}

func (s *SQL) ensureDBSetup() error {
	migrations := &migrate.MemoryMigrationSource{
		Migrations: []*migrate.Migration{
			{
				Id: "init",
				Up: []string{
					fmt.Sprintf(`
						CREATE TABLE %s (
							%s VARCHAR(90),
							%s VARCHAR(64) NOT NULL,
							%s TEXT NOT NULL,
							%s VARCHAR(64) NOT NULL,
							%s VARCHAR(64) NOT NULL,
							%s INTEGER NOT NULL,
							%s TEXT NOT NULL,
							%s TEXT NOT NULL,
							%s INTEGER NOT NULL,
							%s INTEGER NOT NULL DEFAULT 0,
							PRIMARY KEY(%s, %s)
						);
						CREATE INDEX ON %s (%s, %s);
						CREATE INDEX ON %s (%s);
						CREATE INDEX ON %s (%s);
						CREATE INDEX ON %s (%s);
						CREATE INDEX ON %s (%s);
						CREATE INDEX ON %s (%s);

						GRANT ALL ON %s TO PUBLIC;

						ALTER TABLE %s ENABLE ROW LEVEL SECURITY;
					`,
						sqlReleaseTableName,
						sqlReleaseTableKeyColumn,
						sqlReleaseTableTypeColumn,
						sqlReleaseTableBodyColumn,
						sqlReleaseTableNameColumn,
						sqlReleaseTableNamespaceColumn,
						sqlReleaseTableVersionColumn,
						sqlReleaseTableStatusColumn,
						sqlReleaseTableOwnerColumn,
						sqlReleaseTableCreatedAtColumn,
						sqlReleaseTableModifiedAtColumn,
						sqlReleaseTableKeyColumn,
						sqlReleaseTableNamespaceColumn,
						sqlReleaseTableName,
						sqlReleaseTableKeyColumn,
						sqlReleaseTableNamespaceColumn,
						sqlReleaseTableName,
						sqlReleaseTableVersionColumn,
						sqlReleaseTableName,
						sqlReleaseTableStatusColumn,
						sqlReleaseTableName,
						sqlReleaseTableOwnerColumn,
						sqlReleaseTableName,
						sqlReleaseTableCreatedAtColumn,
						sqlReleaseTableName,
						sqlReleaseTableModifiedAtColumn,
						sqlReleaseTableName,
						sqlReleaseTableName,
					),
				},
				Down: []string{
					fmt.Sprintf(`
						DROP TABLE %s;
					`, sqlReleaseTableName),
				},
			},
			{
				Id: "custom_labels",
				Up: []string{
					fmt.Sprintf(`
						CREATE TABLE %s (
							%s VARCHAR(64),
							%s VARCHAR(67),
							%s VARCHAR(%d),
							%s VARCHAR(%d)
						);
						CREATE INDEX ON %s (%s, %s);

						GRANT ALL ON %s TO PUBLIC;
						ALTER TABLE %s ENABLE ROW LEVEL SECURITY;
					`,
						sqlCustomLabelsTableName,
						sqlCustomLabelsTableReleaseKeyColumn,
						sqlCustomLabelsTableReleaseNamespaceColumn,
						sqlCustomLabelsTableKeyColumn,
						sqlCustomLabelsTableKeyMaxLength,
						sqlCustomLabelsTableValueColumn,
						sqlCustomLabelsTableValueMaxLength,
						sqlCustomLabelsTableName,
						sqlCustomLabelsTableReleaseKeyColumn,
						sqlCustomLabelsTableReleaseNamespaceColumn,
						sqlCustomLabelsTableName,
						sqlCustomLabelsTableName,
					),
				},
				Down: []string{
					fmt.Sprintf(`
						DELETE TABLE %s;
					`, sqlCustomLabelsTableName),
				},
			},
		},
	}

	// Check that init migration already applied
	if s.checkAlreadyApplied(migrations.Migrations) {
		return nil
	}

	// Populate the database with the relations we need if they don't exist yet
	_, err := migrate.ExecContext(s.ctx, s.db.DB, postgreSQLDialect, migrations, migrate.Up)
	return err
}

// SQLReleaseWrapper describes how Helm releases are stored in an SQL database
type SQLReleaseWrapper struct {
	// The primary key, made of {release-name}.{release-version}
	Key string `db:"key"`

	// See https://github.com/helm/helm/blob/c9fe3d118caec699eb2565df9838673af379ce12/pkg/storage/driver/secrets.go#L231
	Type string `db:"type"`

	// The rspb.Release body, as a base64-encoded string
	Body string `db:"body"`

	// Release "labels" that can be used as filters in the storage.Query(labels map[string]string)
	// we implemented. Note that allowing Helm users to filter against new dimensions will require a
	// new migration to be added, and the Create and/or update functions to be updated accordingly.
	Name       string `db:"name"`
	Namespace  string `db:"namespace"`
	Version    int    `db:"version"`
	Status     string `db:"status"`
	Owner      string `db:"owner"`
	CreatedAt  int    `db:"createdAt"`
	ModifiedAt int    `db:"modifiedAt"`
}

type SQLReleaseCustomLabelWrapper struct {
	ReleaseKey       string `db:"release_key"`
	ReleaseNamespace string `db:"release_namespace"`
	Key              string `db:"key"`
	Value            string `db:"value"`
}

// NewSQL initializes a new sql driver.
func NewSQL(ctx context.Context, connectionString, namespace string) (*SQL, error) {
	db, err := sqlx.ConnectContext(ctx, postgreSQLDialect, connectionString)
	if err != nil {
		return nil, err
	}

	return withDB(ctx, db, namespace)
}

func withDB(ctx context.Context, db *sqlx.DB, namespace string) (*SQL, error) {
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(1)
	driver := &SQL{ctx: ctx, logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		db:               db,
		statementBuilder: sq.StatementBuilder.PlaceholderFormat(sq.Dollar),
	}

	stop := context.AfterFunc(ctx, func() { _ = db.Close() })
	if err := driver.ensureDBSetup(); err != nil {
		stop()
		_ = db.Close()
		return nil, err
	}

	driver.namespace = namespace

	return driver, nil
}

// Get returns the release named by key.
func (s *SQL) Get(key string) (release.Releaser, error) {
	var record SQLReleaseWrapper

	qb := s.statementBuilder.
		Select(sqlReleaseTableBodyColumn).
		From(sqlReleaseTableName).
		Where(sq.Eq{sqlReleaseTableKeyColumn: key}).
		Where(sq.Eq{sqlReleaseTableNamespaceColumn: s.namespace})

	query, args, err := qb.ToSql()
	if err != nil {
		s.Logger().Debug("failed to build query", slog.Any("error", err))
		return nil, err
	}

	// Get will return an error if the result is empty
	if err := s.db.GetContext(s.ctx, &record, query, args...); err != nil {
		s.Logger().Debug("got SQL error when getting release", slog.String("key", key), slog.Any("error", err))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, driver.ErrReleaseNotFound
		}
		return nil, err
	}

	release, err := decodeRelease(record.Body)
	if err != nil {
		s.Logger().Debug("failed to decode data", slog.String("key", key), slog.Any("error", err))
		return nil, err
	}

	if release.Labels, err = s.getReleaseCustomLabels(key, s.namespace); err != nil {
		s.Logger().Debug(
			"failed to get release custom labels",
			slog.String("namespace", s.namespace),
			slog.String("key", key),
			slog.Any("error", err),
		)
		return nil, err
	}

	return release, nil
}

// List returns the list of all releases such that filter(release) == true
func (s *SQL) List(filter func(release.Releaser) bool) ([]release.Releaser, error) {
	sb := s.statementBuilder.
		Select(sqlReleaseTableKeyColumn, sqlReleaseTableNamespaceColumn, sqlReleaseTableBodyColumn).
		From(sqlReleaseTableName).
		Where(sq.Eq{sqlReleaseTableOwnerColumn: sqlReleaseDefaultOwner})

	// If a namespace was specified, we only list releases from that namespace
	if s.namespace != "" {
		sb = sb.Where(sq.Eq{sqlReleaseTableNamespaceColumn: s.namespace})
	}

	query, args, err := sb.ToSql()
	if err != nil {
		s.Logger().Debug("failed to build query", slog.Any("error", err))
		return nil, err
	}

	records := []SQLReleaseWrapper{}
	if err := s.db.SelectContext(s.ctx, &records, query, args...); err != nil {
		s.Logger().Debug("failed to list", slog.Any("error", err))
		return nil, err
	}

	var releases []release.Releaser
	for _, record := range records {
		release, err := decodeRelease(record.Body)
		if err != nil {
			s.Logger().Debug("failed to decode release", slog.Any("record", record), slog.Any("error", err))
			continue
		}

		if release.Labels, err = s.getReleaseCustomLabels(record.Key, record.Namespace); err != nil {
			s.Logger().Debug(
				"failed to get release custom labels",
				slog.String("namespace", record.Namespace),
				slog.String("key", record.Key),
				slog.Any("error", err),
			)
			return nil, err
		}
		maps.Copy(release.Labels, getReleaseSystemLabels(release))

		if filter(release) {
			releases = append(releases, release)
		}
	}

	return releases, nil
}

// Query returns the set of releases that match the provided set of labels.
func (s *SQL) Query(labels map[string]string) ([]release.Releaser, error) {
	sb := s.statementBuilder.
		Select(sqlReleaseTableKeyColumn, sqlReleaseTableNamespaceColumn, sqlReleaseTableBodyColumn).
		From(sqlReleaseTableName)

	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		_, ok := labelMap[key]
		if !ok {
			s.Logger().Debug("unknown label", "key", key)
			return nil, fmt.Errorf("unknown label %s", key)
		}
		sb = sb.Where(sq.Eq{key: labels[key]})
	}

	// If a namespace was specified, we only list releases from that namespace
	if s.namespace != "" {
		sb = sb.Where(sq.Eq{sqlReleaseTableNamespaceColumn: s.namespace})
	}

	// Build our query
	query, args, err := sb.ToSql()
	if err != nil {
		s.Logger().Debug("failed to build query", slog.Any("error", err))
		return nil, err
	}

	records := []SQLReleaseWrapper{}
	if err := s.db.SelectContext(s.ctx, &records, query, args...); err != nil {
		s.Logger().Debug("failed to query with labels", slog.Any("error", err))
		return nil, err
	}

	if len(records) == 0 {
		return nil, driver.ErrReleaseNotFound
	}

	var releases []release.Releaser
	for _, record := range records {
		release, err := decodeRelease(record.Body)
		if err != nil {
			s.Logger().Debug("failed to decode release", slog.Any("record", record), slog.Any("error", err))
			continue
		}

		if release.Labels, err = s.getReleaseCustomLabels(record.Key, record.Namespace); err != nil {
			s.Logger().Debug(
				"failed to get release custom labels",
				slog.String("namespace", record.Namespace),
				slog.String("key", record.Key),
				slog.Any("error", err),
			)
			return nil, err
		}

		releases = append(releases, release)
	}

	if len(releases) == 0 {
		return nil, driver.ErrReleaseNotFound
	}

	return releases, nil
}

// Create creates a new release.
func (s *SQL) Create(key string, rel release.Releaser) error {
	rls, err := releaserToV1Release(rel)
	if err != nil {
		return err
	}

	namespace := rls.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	s.namespace = namespace

	body, err := encodeRelease(rls)
	if err != nil {
		s.Logger().Debug("failed to encode release", slog.Any("error", err))
		return err
	}

	transaction, err := s.db.BeginTxx(s.ctx, nil)
	if err != nil {
		s.Logger().Debug("failed to start SQL transaction", slog.Any("error", err))
		return fmt.Errorf("error beginning transaction: %w", err)
	}

	defer func() { _ = transaction.Rollback() }()
	insertQuery, args, err := s.statementBuilder.
		Insert(sqlReleaseTableName).
		Columns(
			sqlReleaseTableKeyColumn,
			sqlReleaseTableTypeColumn,
			sqlReleaseTableBodyColumn,
			sqlReleaseTableNameColumn,
			sqlReleaseTableNamespaceColumn,
			sqlReleaseTableVersionColumn,
			sqlReleaseTableStatusColumn,
			sqlReleaseTableOwnerColumn,
			sqlReleaseTableCreatedAtColumn,
		).
		Values(
			key,
			sqlReleaseDefaultType,
			body,
			rls.Name,
			namespace,
			int(rls.Version),
			rls.Info.Status.String(),
			sqlReleaseDefaultOwner,
			int(time.Now().Unix()),
		).ToSql()
	if err != nil {
		s.Logger().Debug("failed to build insert query", slog.Any("error", err))
		return err
	}

	if _, err := transaction.ExecContext(s.ctx, insertQuery, args...); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return driver.ErrReleaseExists
		}
		return err
	}

	// Filtering labels before insert cause in SQL storage driver system releases are stored in separate columns of release table
	for k, v := range filterSystemLabels(rls.Labels) {
		insertLabelsQuery, args, err := s.statementBuilder.
			Insert(sqlCustomLabelsTableName).
			Columns(
				sqlCustomLabelsTableReleaseKeyColumn,
				sqlCustomLabelsTableReleaseNamespaceColumn,
				sqlCustomLabelsTableKeyColumn,
				sqlCustomLabelsTableValueColumn,
			).
			Values(
				key,
				namespace,
				k,
				v,
			).ToSql()
		if err != nil {
			defer func() { _ = transaction.Rollback() }()
			s.Logger().Debug("failed to build insert query", slog.Any("error", err))
			return err
		}

		if _, err := transaction.ExecContext(s.ctx, insertLabelsQuery, args...); err != nil {
			defer func() { _ = transaction.Rollback() }()
			s.Logger().Debug("failed to write Labels", slog.Any("error", err))
			return err
		}
	}
	return transaction.Commit()
}

// Update updates a release.
func (s *SQL) Update(key string, rel release.Releaser) error {
	rls, err := releaserToV1Release(rel)
	if err != nil {
		return err
	}
	namespace := rls.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	s.namespace = namespace

	body, err := encodeRelease(rls)
	if err != nil {
		s.Logger().Debug("failed to encode release", slog.Any("error", err))
		return err
	}

	query, args, err := s.statementBuilder.
		Update(sqlReleaseTableName).
		Set(sqlReleaseTableBodyColumn, body).
		Set(sqlReleaseTableNameColumn, rls.Name).
		Set(sqlReleaseTableVersionColumn, int(rls.Version)).
		Set(sqlReleaseTableStatusColumn, rls.Info.Status.String()).
		Set(sqlReleaseTableOwnerColumn, sqlReleaseDefaultOwner).
		Set(sqlReleaseTableModifiedAtColumn, int(time.Now().Unix())).
		Where(sq.Eq{sqlReleaseTableKeyColumn: key}).
		Where(sq.Eq{sqlReleaseTableNamespaceColumn: namespace}).
		ToSql()
	if err != nil {
		s.Logger().Debug("failed to build update query", slog.Any("error", err))
		return err
	}

	if _, err := s.db.ExecContext(s.ctx, query, args...); err != nil {
		s.Logger().Debug("failed to update release in SQL database", slog.String("key", key), slog.Any("error", err))
		return err
	}

	return nil
}

// Delete deletes a release or returns ErrReleaseNotFound.
func (s *SQL) Delete(key string) (release.Releaser, error) {
	transaction, err := s.db.BeginTxx(s.ctx, nil)
	if err != nil {
		s.Logger().Debug("failed to start SQL transaction", slog.Any("error", err))
		return nil, fmt.Errorf("error beginning transaction: %w", err)
	}

	defer func() { _ = transaction.Rollback() }()
	selectQuery, args, err := s.statementBuilder.
		Select(sqlReleaseTableBodyColumn).
		From(sqlReleaseTableName).
		Where(sq.Eq{sqlReleaseTableKeyColumn: key}).
		Where(sq.Eq{sqlReleaseTableNamespaceColumn: s.namespace}).
		ToSql()
	if err != nil {
		s.Logger().Debug("failed to build select query", slog.Any("error", err))
		return nil, err
	}

	var record SQLReleaseWrapper
	err = transaction.GetContext(s.ctx, &record, selectQuery, args...)
	if err != nil {
		s.Logger().Debug("release not found", slog.String("key", key), slog.Any("error", err))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, driver.ErrReleaseNotFound
		}
		return nil, err
	}

	release, err := decodeRelease(record.Body)
	if err != nil {
		s.Logger().Debug("failed to decode release", slog.String("key", key), slog.Any("error", err))
		_ = transaction.Rollback()
		return nil, err
	}

	deleteQuery, args, err := s.statementBuilder.
		Delete(sqlReleaseTableName).
		Where(sq.Eq{sqlReleaseTableKeyColumn: key}).
		Where(sq.Eq{sqlReleaseTableNamespaceColumn: s.namespace}).
		ToSql()
	if err != nil {
		s.Logger().Debug("failed to build delete query", slog.Any("error", err))
		return nil, err
	}

	_, err = transaction.ExecContext(s.ctx, deleteQuery, args...)
	if err != nil {
		s.Logger().Debug("failed perform delete query", slog.Any("error", err))
		return release, err
	}

	if release.Labels, err = s.getReleaseCustomLabels(key, s.namespace); err != nil {
		s.Logger().Debug(
			"failed to get release custom labels",
			slog.String("namespace", s.namespace),
			slog.String("key", key),
			slog.Any("error", err))
		return nil, err
	}

	deleteCustomLabelsQuery, args, err := s.statementBuilder.
		Delete(sqlCustomLabelsTableName).
		Where(sq.Eq{sqlCustomLabelsTableReleaseKeyColumn: key}).
		Where(sq.Eq{sqlCustomLabelsTableReleaseNamespaceColumn: s.namespace}).
		ToSql()
	if err != nil {
		s.Logger().Debug("failed to build delete Labels query", slog.Any("error", err))
		return nil, err
	}
	if _, err = transaction.ExecContext(s.ctx, deleteCustomLabelsQuery, args...); err != nil {
		return release, err
	}
	return release, transaction.Commit()
}

// Get release custom labels from database
func (s *SQL) getReleaseCustomLabels(key, _ string) (map[string]string, error) {
	query, args, err := s.statementBuilder.
		Select(sqlCustomLabelsTableKeyColumn, sqlCustomLabelsTableValueColumn).
		From(sqlCustomLabelsTableName).
		Where(sq.Eq{
			sqlCustomLabelsTableReleaseKeyColumn:       key,
			sqlCustomLabelsTableReleaseNamespaceColumn: s.namespace,
		}).
		ToSql()
	if err != nil {
		return nil, err
	}

	labelsList := []SQLReleaseCustomLabelWrapper{}
	if err := s.db.SelectContext(s.ctx, &labelsList, query, args...); err != nil {
		return nil, err
	}

	labelsMap := make(map[string]string)
	for _, i := range labelsList {
		labelsMap[i.Key] = i.Value
	}

	return filterSystemLabels(labelsMap), nil
}

// Rebuild system labels from release object
func getReleaseSystemLabels(rls *rspb.Release) map[string]string {
	return map[string]string{
		"name":    rls.Name,
		"owner":   sqlReleaseDefaultOwner,
		"status":  rls.Info.Status.String(),
		"version": strconv.Itoa(rls.Version),
	}
}

func (s *SQL) Logger() *slog.Logger     { return s.logger }
func (s *SQL) SetLogger(h slog.Handler) { s.logger = slog.New(h) }

const defaultNamespace = "default"

func releaserToV1Release(raw release.Releaser) (*rspb.Release, error) {
	switch r := raw.(type) {
	case *rspb.Release:
		if r != nil {
			return r, nil
		}
	case rspb.Release:
		return &r, nil
	}
	return nil, fmt.Errorf("unsupported release type: %T", raw)
}
