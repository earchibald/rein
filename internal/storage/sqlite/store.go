package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "modernc.org/sqlite"

	"github.com/earchibald/rein/internal/settings"
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
	ErrAmbiguousSettings    = errors.New("sqlite: cannot migrate multiple legacy settings rows to scoped settings")
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

type ListOptions struct {
	JSONEquals map[string]string
	Limit      int
}

type SettingsProfile struct {
	Layer       settings.Layer
	ScopeID     string
	Values      map[string]string
	LockVersion int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SettingsResolutionScope struct {
	ProjectID   string
	WorkflowID  string
	ExecutionID string
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

	if err := validateLegacySettingsUpgrade(ctx, migrationDB, normalized.MigrationsTable); err != nil {
		_ = migrationDB.Close()
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

func (s *Store) List(ctx context.Context, kind EntityKind, options ListOptions) ([]Record, error) {
	table, err := tableFor(kind)
	if err != nil {
		return nil, err
	}

	// #nosec G201 -- table is selected from the fixed entity kind map via tableFor.
	query := fmt.Sprintf(`SELECT id, lock_version, payload, created_at, updated_at FROM %s`, table)
	args := make([]any, 0, len(options.JSONEquals)*2+1)
	if len(options.JSONEquals) > 0 {
		keys := make([]string, 0, len(options.JSONEquals))
		for key := range options.JSONEquals {
			keys = append(keys, key)
		}
		slices.Sort(keys)

		clauses := make([]string, 0, len(keys))
		for _, key := range keys {
			clauses = append(clauses, `json_extract(payload, ?) = ?`)
			args = append(args, "$."+key, options.JSONEquals[key])
		}
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += " ORDER BY created_at ASC"
	if options.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, options.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list %s: %w", kind, err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var (
			record       Record
			createdAtRaw string
			updatedAtRaw string
		)
		if err := rows.Scan(&record.ID, &record.LockVersion, &record.Payload, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, fmt.Errorf("sqlite: scan %s row: %w", kind, err)
		}
		record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse created_at for %s %q: %w", kind, record.ID, err)
		}
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse updated_at for %s %q: %w", kind, record.ID, err)
		}
		record.Payload = clonePayload(record.Payload)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate %s rows: %w", kind, err)
	}

	return records, nil
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

func (s *Store) CreateSettingsProfile(ctx context.Context, registry settings.Registry, layer settings.Layer, scopeID string, values map[string]string) (SettingsProfile, error) {
	normalizedScopeID, payload, normalizedValues, err := prepareSettingsPayload(registry, layer, scopeID, values)
	if err != nil {
		return SettingsProfile{}, err
	}

	now := time.Now().UTC()
	encodedNow := now.Format(time.RFC3339Nano)

	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO settings (id, scope_layer, scope_id, lock_version, payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		settingsProfileID(layer, normalizedScopeID),
		string(layer),
		normalizedScopeID,
		1,
		payload,
		encodedNow,
		encodedNow,
	); err != nil {
		return SettingsProfile{}, fmt.Errorf("sqlite: create settings %q/%q: %w", layer, normalizedScopeID, err)
	}

	return SettingsProfile{
		Layer:       layer,
		ScopeID:     normalizedScopeID,
		Values:      normalizedValues,
		LockVersion: 1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (s *Store) GetSettingsProfile(ctx context.Context, layer settings.Layer, scopeID string) (SettingsProfile, error) {
	normalizedScopeID, err := settings.NormalizeScopeID(layer, scopeID)
	if err != nil {
		return SettingsProfile{}, err
	}

	var (
		profile      SettingsProfile
		payload      []byte
		createdAtRaw string
		updatedAtRaw string
	)

	err = s.db.QueryRowContext(
		ctx,
		`SELECT scope_layer, scope_id, lock_version, payload, created_at, updated_at FROM settings WHERE scope_layer = ? AND scope_id = ?`,
		string(layer),
		normalizedScopeID,
	).Scan(&profile.Layer, &profile.ScopeID, &profile.LockVersion, &payload, &createdAtRaw, &updatedAtRaw)
	if errors.Is(err, sql.ErrNoRows) {
		return SettingsProfile{}, ErrNotFound
	}
	if err != nil {
		return SettingsProfile{}, fmt.Errorf("sqlite: get settings %q/%q: %w", layer, normalizedScopeID, err)
	}

	profile.Values, err = decodeSettingsValues(payload)
	if err != nil {
		return SettingsProfile{}, err
	}
	profile.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtRaw)
	if err != nil {
		return SettingsProfile{}, fmt.Errorf("sqlite: parse created_at for settings %q/%q: %w", layer, normalizedScopeID, err)
	}
	profile.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtRaw)
	if err != nil {
		return SettingsProfile{}, fmt.Errorf("sqlite: parse updated_at for settings %q/%q: %w", layer, normalizedScopeID, err)
	}

	return profile, nil
}

func (s *Store) UpdateSettingsProfile(ctx context.Context, registry settings.Registry, layer settings.Layer, scopeID string, expectedLockVersion int64, values map[string]string) (SettingsProfile, error) {
	if expectedLockVersion < 1 {
		return SettingsProfile{}, ErrLockVersionMismatch
	}

	normalizedScopeID, payload, _, err := prepareSettingsPayload(registry, layer, scopeID, values)
	if err != nil {
		return SettingsProfile{}, err
	}

	encodedNow := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE settings SET payload = ?, lock_version = lock_version + 1, updated_at = ? WHERE scope_layer = ? AND scope_id = ? AND lock_version = ?`,
		payload,
		encodedNow,
		string(layer),
		normalizedScopeID,
		expectedLockVersion,
	)
	if err != nil {
		return SettingsProfile{}, fmt.Errorf("sqlite: update settings %q/%q: %w", layer, normalizedScopeID, err)
	}

	if err := s.requireSettingsVersionedWrite(ctx, layer, normalizedScopeID, expectedLockVersion, result); err != nil {
		return SettingsProfile{}, err
	}

	return s.GetSettingsProfile(ctx, layer, normalizedScopeID)
}

func (s *Store) DeleteSettingsProfile(ctx context.Context, layer settings.Layer, scopeID string, expectedLockVersion int64) error {
	if expectedLockVersion < 1 {
		return ErrLockVersionMismatch
	}

	normalizedScopeID, err := settings.NormalizeScopeID(layer, scopeID)
	if err != nil {
		return err
	}

	result, err := s.db.ExecContext(
		ctx,
		`DELETE FROM settings WHERE scope_layer = ? AND scope_id = ? AND lock_version = ?`,
		string(layer),
		normalizedScopeID,
		expectedLockVersion,
	)
	if err != nil {
		return fmt.Errorf("sqlite: delete settings %q/%q: %w", layer, normalizedScopeID, err)
	}

	return s.requireSettingsVersionedWrite(ctx, layer, normalizedScopeID, expectedLockVersion, result)
}

func (s *Store) ResolveSettings(ctx context.Context, registry settings.Registry, scope SettingsResolutionScope) (map[string]settings.ResolvedValue, error) {
	var profiles []settings.ScopedValues

	daemonProfile, err := s.GetSettingsProfile(ctx, settings.LayerDaemonGlobal, "")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if err == nil {
		profiles = append(profiles, settings.ScopedValues{
			Layer:   daemonProfile.Layer,
			ScopeID: daemonProfile.ScopeID,
			Values:  daemonProfile.Values,
		})
	}

	for _, target := range []struct {
		layer   settings.Layer
		scopeID string
	}{
		{layer: settings.LayerProject, scopeID: scope.ProjectID},
		{layer: settings.LayerWorkflow, scopeID: scope.WorkflowID},
		{layer: settings.LayerExecution, scopeID: scope.ExecutionID},
	} {
		if target.scopeID == "" {
			continue
		}

		profile, err := s.GetSettingsProfile(ctx, target.layer, target.scopeID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}

		profiles = append(profiles, settings.ScopedValues{
			Layer:   profile.Layer,
			ScopeID: profile.ScopeID,
			Values:  profile.Values,
		})
	}

	return settings.Resolve(registry, profiles...)
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

func cloneSettingsValues(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func decodeSettingsValues(payload []byte) (map[string]string, error) {
	values := make(map[string]string)
	if err := json.Unmarshal(payload, &values); err != nil {
		return nil, fmt.Errorf("sqlite: decode settings payload: %w", err)
	}

	return cloneSettingsValues(values), nil
}

func prepareSettingsPayload(registry settings.Registry, layer settings.Layer, scopeID string, values map[string]string) (normalizedScopeID string, payload []byte, normalizedValues map[string]string, err error) {
	normalizedScopeID, err = settings.NormalizeScopeID(layer, scopeID)
	if err != nil {
		return "", nil, nil, err
	}

	normalizedValues = cloneSettingsValues(values)
	if err := registry.Validate(layer, normalizedValues); err != nil {
		return "", nil, nil, err
	}

	payload, err = json.Marshal(normalizedValues)
	if err != nil {
		return "", nil, nil, fmt.Errorf("sqlite: encode settings payload: %w", err)
	}

	return normalizedScopeID, payload, normalizedValues, nil
}

func settingsProfileID(layer settings.Layer, scopeID string) string {
	return fmt.Sprintf("%s:%s", layer, scopeID)
}

func (s *Store) requireSettingsVersionedWrite(ctx context.Context, layer settings.Layer, scopeID string, expectedLockVersion int64, result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: inspect settings write result for %q/%q: %w", layer, scopeID, err)
	}
	if rowsAffected > 0 {
		return nil
	}

	var currentLockVersion int64
	err = s.db.QueryRowContext(
		ctx,
		`SELECT lock_version FROM settings WHERE scope_layer = ? AND scope_id = ?`,
		string(layer),
		scopeID,
	).Scan(&currentLockVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: inspect current settings lock version for %q/%q: %w", layer, scopeID, err)
	}
	if currentLockVersion != expectedLockVersion {
		return ErrLockVersionMismatch
	}

	return ErrNotFound
}
