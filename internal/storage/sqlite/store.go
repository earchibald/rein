package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "modernc.org/sqlite"
)

const (
	driverName             = "sqlite"
	defaultBusyTimeout     = 5 * time.Second
	defaultMigrationsTable = "schema_migrations"
)

var (
	ErrMissingPath          = errors.New("sqlite: missing database path")
	ErrUnknownKind          = errors.New("sqlite: unknown entity kind")
	ErrEmptyID              = errors.New("sqlite: empty record id")
	ErrInvalidPayload       = errors.New("sqlite: invalid JSON payload")
	ErrNotFound             = errors.New("sqlite: record not found")
	ErrLockVersionMismatch  = errors.New("sqlite: lock version mismatch")
	ErrInvalidMigrationStep = errors.New("sqlite: migration steps must be positive")
)

type EntityKind string

const (
	ProjectKind     EntityKind = "project"
	WorkflowKind    EntityKind = "workflow"
	IssueKind       EntityKind = "issue"
	ExecutionKind   EntityKind = "execution"
	TaskStepKind    EntityKind = "taskstep"
	SideEffectKind  EntityKind = "sideeffect"
	CostEventKind   EntityKind = "costevent"
	SettingsKind    EntityKind = "settings"
	FeatureFlagKind EntityKind = "featureflag"
)

var entityTables = map[EntityKind]string{
	ProjectKind:     "projects",
	WorkflowKind:    "workflows",
	IssueKind:       "issues",
	ExecutionKind:   "executions",
	TaskStepKind:    "tasksteps",
	SideEffectKind:  "sideeffects",
	CostEventKind:   "costevents",
	SettingsKind:    "settings",
	FeatureFlagKind: "featureflags",
}

type Config struct {
	Path            string
	MigrationsTable string
	BusyTimeout     time.Duration
}

type Record struct {
	ID          string
	LockVersion int64
	Payload     json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Store struct {
	db     *sql.DB
	config Config
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return nil, err
	}

	db, err := openDB(ctx, normalized)
	if err != nil {
		return nil, err
	}

	return &Store{
		db:     db,
		config: normalized,
	}, nil
}

func OpenAndMigrate(ctx context.Context, cfg Config) (*Store, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return nil, err
	}

	migrationDB, err := openDB(ctx, normalized)
	if err != nil {
		return nil, err
	}

	migrator, err := newMigrator(normalized, migrationDB)
	if err != nil {
		_ = migrationDB.Close()
		return nil, err
	}

	if err := applyMigrator(migrator, func(m *migrate.Migrate) error {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	}); err != nil {
		sourceErr, closeErr := migrator.Close()
		err = errors.Join(err, sourceErr, closeErr)
		return nil, err
	}

	db, err := openDB(ctx, normalized)
	if err != nil {
		sourceErr, closeErr := migrator.Close()
		err = errors.Join(err, sourceErr, closeErr)
		return nil, err
	}

	sourceErr, closeErr := migrator.Close()
	if sourceErr != nil || closeErr != nil {
		_ = db.Close()
		return nil, errors.Join(sourceErr, closeErr)
	}

	return &Store{
		db:     db,
		config: normalized,
	}, nil
}

func InMemoryConfig(name string) Config {
	if name == "" {
		name = "rein"
	}

	return Config{
		Path: fmt.Sprintf("file:%s?mode=memory&cache=shared", url.PathEscape(name)),
	}
}

func OpenInMemoryAndMigrate(ctx context.Context, name string) (*Store, error) {
	return OpenAndMigrate(ctx, InMemoryConfig(name))
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Create(ctx context.Context, kind EntityKind, id string, payload json.RawMessage) (Record, error) {
	table, err := tableFor(kind)
	if err != nil {
		return Record{}, err
	}
	if id == "" {
		return Record{}, ErrEmptyID
	}
	if !json.Valid(payload) {
		return Record{}, ErrInvalidPayload
	}

	now := time.Now().UTC()
	encodedNow := now.Format(time.RFC3339Nano)

	if _, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(`INSERT INTO %s (id, lock_version, payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`, table),
		id,
		1,
		[]byte(payload),
		encodedNow,
		encodedNow,
	); err != nil {
		return Record{}, fmt.Errorf("sqlite: create %s %q: %w", kind, id, err)
	}

	return Record{
		ID:          id,
		LockVersion: 1,
		Payload:     clonePayload(payload),
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Store) Get(ctx context.Context, kind EntityKind, id string) (Record, error) {
	table, err := tableFor(kind)
	if err != nil {
		return Record{}, err
	}
	if id == "" {
		return Record{}, ErrEmptyID
	}

	var (
		record       Record
		createdAtRaw string
		updatedAtRaw string
	)

	err = s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT id, lock_version, payload, created_at, updated_at FROM %s WHERE id = ?`, table),
		id,
	).Scan(&record.ID, &record.LockVersion, &record.Payload, &createdAtRaw, &updatedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("sqlite: get %s %q: %w", kind, id, err)
	}

	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return Record{}, fmt.Errorf("sqlite: parse created_at for %s %q: %w", kind, id, err)
	}

	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtRaw)
	if err != nil {
		return Record{}, fmt.Errorf("sqlite: parse updated_at for %s %q: %w", kind, id, err)
	}

	record.Payload = clonePayload(record.Payload)

	return record, nil
}

func (s *Store) Update(ctx context.Context, kind EntityKind, id string, expectedLockVersion int64, payload json.RawMessage) (Record, error) {
	table, err := tableFor(kind)
	if err != nil {
		return Record{}, err
	}
	if id == "" {
		return Record{}, ErrEmptyID
	}
	if expectedLockVersion < 1 {
		return Record{}, ErrLockVersionMismatch
	}
	if !json.Valid(payload) {
		return Record{}, ErrInvalidPayload
	}

	now := time.Now().UTC()
	encodedNow := now.Format(time.RFC3339Nano)

	result, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET payload = ?, lock_version = lock_version + 1, updated_at = ? WHERE id = ? AND lock_version = ?`, table),
		[]byte(payload),
		encodedNow,
		id,
		expectedLockVersion,
	)
	if err != nil {
		return Record{}, fmt.Errorf("sqlite: update %s %q: %w", kind, id, err)
	}

	if err := s.requireVersionedWrite(ctx, table, id, expectedLockVersion, result); err != nil {
		return Record{}, err
	}

	record, err := s.Get(ctx, kind, id)
	if err != nil {
		return Record{}, err
	}

	return record, nil
}

func (s *Store) Delete(ctx context.Context, kind EntityKind, id string, expectedLockVersion int64) error {
	table, err := tableFor(kind)
	if err != nil {
		return err
	}
	if id == "" {
		return ErrEmptyID
	}
	if expectedLockVersion < 1 {
		return ErrLockVersionMismatch
	}

	result, err := s.db.ExecContext(
		ctx,
		fmt.Sprintf(`DELETE FROM %s WHERE id = ? AND lock_version = ?`, table),
		id,
		expectedLockVersion,
	)
	if err != nil {
		return fmt.Errorf("sqlite: delete %s %q: %w", kind, id, err)
	}

	return s.requireVersionedWrite(ctx, table, id, expectedLockVersion, result)
}

func (s *Store) requireVersionedWrite(ctx context.Context, table, id string, expectedLockVersion int64, result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: inspect write result for %q: %w", id, err)
	}
	if rowsAffected > 0 {
		return nil
	}

	var currentLockVersion int64
	err = s.db.QueryRowContext(
		ctx,
		fmt.Sprintf(`SELECT lock_version FROM %s WHERE id = ?`, table),
		id,
	).Scan(&currentLockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: inspect current lock version for %q: %w", id, err)
	}
	if currentLockVersion != expectedLockVersion {
		return ErrLockVersionMismatch
	}

	return ErrNotFound
}

func tableFor(kind EntityKind) (string, error) {
	table, ok := entityTables[kind]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}

	return table, nil
}

func openDB(ctx context.Context, cfg Config) (*sql.DB, error) {
	db, err := sql.Open(driverName, cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", cfg.Path, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping %q: %w", cfg.Path, err)
	}

	if err := applyPragmas(ctx, db, cfg); err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

func applyPragmas(ctx context.Context, db *sql.DB, cfg Config) error {
	journalMode := "WAL"
	if isInMemoryPath(cfg.Path) {
		journalMode = "MEMORY"
	}

	statements := []string{
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeout.Milliseconds()),
		fmt.Sprintf("PRAGMA journal_mode = %s", journalMode),
		"PRAGMA synchronous = NORMAL",
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlite: apply %q: %w", statement, err)
		}
	}

	return nil
}

func isInMemoryPath(path string) bool {
	return path == ":memory:" || strings.Contains(path, "mode=memory")
}

func (c Config) normalize() (Config, error) {
	if c.Path == "" {
		return Config{}, ErrMissingPath
	}
	if c.MigrationsTable == "" {
		c.MigrationsTable = defaultMigrationsTable
	}
	if c.BusyTimeout <= 0 {
		c.BusyTimeout = defaultBusyTimeout
	}

	return c, nil
}

func clonePayload(payload json.RawMessage) json.RawMessage {
	if payload == nil {
		return nil
	}

	return append(json.RawMessage(nil), payload...)
}
