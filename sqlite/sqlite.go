package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	storesqlite "github.com/earchibald/rein/internal/storage/sqlite"
)

var (
	ErrMissingPath          = storesqlite.ErrMissingPath
	ErrUnknownKind          = storesqlite.ErrUnknownKind
	ErrEmptyID              = storesqlite.ErrEmptyID
	ErrInvalidPayload       = storesqlite.ErrInvalidPayload
	ErrNotFound             = storesqlite.ErrNotFound
	ErrLockVersionMismatch  = storesqlite.ErrLockVersionMismatch
	ErrInvalidMigrationStep = storesqlite.ErrInvalidMigrationStep
)

type EntityKind string

const (
	ProjectKind     EntityKind = EntityKind(storesqlite.ProjectKind)
	WorkflowKind    EntityKind = EntityKind(storesqlite.WorkflowKind)
	IssueKind       EntityKind = EntityKind(storesqlite.IssueKind)
	ExecutionKind   EntityKind = EntityKind(storesqlite.ExecutionKind)
	TaskStepKind    EntityKind = EntityKind(storesqlite.TaskStepKind)
	SideEffectKind  EntityKind = EntityKind(storesqlite.SideEffectKind)
	CostEventKind   EntityKind = EntityKind(storesqlite.CostEventKind)
	FeatureFlagKind EntityKind = EntityKind(storesqlite.FeatureFlagKind)
)

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

type ListOptions struct {
	JSONEquals map[string]string
	Limit      int
}

type Store struct {
	inner *storesqlite.Store
}

func Open(ctx context.Context, cfg Config) (*Store, error) {
	store, err := storesqlite.Open(ctx, toInternalConfig(cfg))
	if err != nil {
		return nil, err
	}
	return &Store{inner: store}, nil
}

func OpenAndMigrate(ctx context.Context, cfg Config) (*Store, error) {
	store, err := storesqlite.OpenAndMigrate(ctx, toInternalConfig(cfg))
	if err != nil {
		return nil, err
	}
	return &Store{inner: store}, nil
}

func MigrateUp(ctx context.Context, cfg Config) error {
	return storesqlite.MigrateUp(ctx, toInternalConfig(cfg))
}

func MigrateDown(ctx context.Context, cfg Config) error {
	return storesqlite.MigrateDown(ctx, toInternalConfig(cfg))
}

func MigrateDownSteps(ctx context.Context, cfg Config, steps int) error {
	return storesqlite.MigrateDownSteps(ctx, toInternalConfig(cfg), steps)
}

func InMemoryConfig(name string) Config {
	return fromInternalConfig(storesqlite.InMemoryConfig(name))
}

func OpenInMemoryAndMigrate(ctx context.Context, name string) (*Store, error) {
	store, err := storesqlite.OpenInMemoryAndMigrate(ctx, name)
	if err != nil {
		return nil, err
	}
	return &Store{inner: store}, nil
}

func (s *Store) Close() error {
	return s.inner.Close()
}

func (s *Store) DB() *sql.DB {
	return s.inner.DB()
}

func (s *Store) Create(ctx context.Context, kind EntityKind, id string, payload json.RawMessage) (Record, error) {
	record, err := s.inner.Create(ctx, toInternalKind(kind), id, payload)
	if err != nil {
		return Record{}, err
	}
	return fromInternalRecord(record), nil
}

func (s *Store) Get(ctx context.Context, kind EntityKind, id string) (Record, error) {
	record, err := s.inner.Get(ctx, toInternalKind(kind), id)
	if err != nil {
		return Record{}, err
	}
	return fromInternalRecord(record), nil
}

func (s *Store) Update(ctx context.Context, kind EntityKind, id string, expectedLockVersion int64, payload json.RawMessage) (Record, error) {
	record, err := s.inner.Update(ctx, toInternalKind(kind), id, expectedLockVersion, payload)
	if err != nil {
		return Record{}, err
	}
	return fromInternalRecord(record), nil
}

func (s *Store) List(ctx context.Context, kind EntityKind, options ListOptions) ([]Record, error) {
	records, err := s.inner.List(ctx, toInternalKind(kind), toInternalListOptions(options))
	if err != nil {
		return nil, err
	}
	result := make([]Record, 0, len(records))
	for _, record := range records {
		result = append(result, fromInternalRecord(record))
	}
	return result, nil
}

func (s *Store) Delete(ctx context.Context, kind EntityKind, id string, expectedLockVersion int64) error {
	return s.inner.Delete(ctx, toInternalKind(kind), id, expectedLockVersion)
}

func toInternalKind(kind EntityKind) storesqlite.EntityKind {
	return storesqlite.EntityKind(kind)
}

func toInternalConfig(cfg Config) storesqlite.Config {
	return storesqlite.Config{
		Path:            cfg.Path,
		MigrationsTable: cfg.MigrationsTable,
		BusyTimeout:     cfg.BusyTimeout,
	}
}

func fromInternalConfig(cfg storesqlite.Config) Config {
	return Config{
		Path:            cfg.Path,
		MigrationsTable: cfg.MigrationsTable,
		BusyTimeout:     cfg.BusyTimeout,
	}
}

func fromInternalRecord(record storesqlite.Record) Record {
	return Record{
		ID:          record.ID,
		LockVersion: record.LockVersion,
		Payload:     append(json.RawMessage(nil), record.Payload...),
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
}

func toInternalListOptions(options ListOptions) storesqlite.ListOptions {
	cloned := make(map[string]string, len(options.JSONEquals))
	for key, value := range options.JSONEquals {
		cloned[key] = value
	}
	return storesqlite.ListOptions{
		JSONEquals: cloned,
		Limit:      options.Limit,
	}
}
