package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/earchibald/rein/internal/settings"
)

func TestMigrateUpAndDown(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "rein.db"),
	}

	if err := MigrateUp(ctx, cfg); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after up error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	for _, table := range []string{
		"projects",
		"workflows",
		"issues",
		"executions",
		"tasksteps",
		"sideeffects",
		"costevents",
		"costeventlog",
		"budgetstates",
		"settings",
		"featureflags",
	} {
		assertTableExists(t, db.DB(), table, true)
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before down error = %v", err)
	}

	if err := MigrateDown(ctx, cfg); err != nil {
		t.Fatalf("MigrateDown() error = %v", err)
	}

	db, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after down error = %v", err)
	}

	for _, table := range []string{
		"projects",
		"workflows",
		"issues",
		"executions",
		"tasksteps",
		"sideeffects",
		"costevents",
		"costeventlog",
		"budgetstates",
		"settings",
		"featureflags",
	} {
		assertTableExists(t, db.DB(), table, false)
	}
}

func TestMigrateDownStepsRejectsInvalidStepCount(t *testing.T) {
	t.Parallel()

	if err := MigrateDownSteps(context.Background(), Config{Path: "rein.db"}, 0); err != ErrInvalidMigrationStep {
		t.Fatalf("MigrateDownSteps() error = %v, want %v", err, ErrInvalidMigrationStep)
	}
}

func TestMigrateDownStepsRollsBackLatestMigration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := InMemoryConfig(t.Name())

	store, err := OpenAndMigrate(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}

	if err := MigrateDownSteps(ctx, cfg, 1); err != nil {
		t.Fatalf("MigrateDownSteps() error = %v", err)
	}

	assertTableExists(t, store.DB(), "projects", true)
	assertTableExists(t, store.DB(), "costeventlog", true)
	assertTableExists(t, store.DB(), "budgetstates", true)
	assertTableHasColumns(t, store.DB(), "settings", false, "scope_layer", "scope_id")

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestMigrateUpUpgradesLegacySettingsSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "legacy-settings.db"),
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	legacyStatements := []string{
		`CREATE TABLE settings (
			id TEXT PRIMARY KEY,
			lock_version INTEGER NOT NULL CHECK (lock_version > 0),
			payload TEXT NOT NULL CHECK (json_valid(payload)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_settings_updated_at ON settings(updated_at)`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`CREATE TABLE schema_migrations (version uint64,dirty bool)`,
		`INSERT INTO schema_migrations(version, dirty) VALUES (1, 0)`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before MigrateUp error = %v", err)
	}

	if err := MigrateUp(ctx, cfg); err != nil {
		t.Fatalf("MigrateUp() upgrade error = %v", err)
	}

	db, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after upgrade error = %v", err)
	}

	assertTableHasColumns(t, db.DB(), "settings", true, "scope_layer", "scope_id")

	var scopeLayer, scopeID string
	err = db.DB().QueryRowContext(ctx, `SELECT scope_layer, scope_id FROM settings WHERE id = 'daemon-global:daemon-global'`).Scan(&scopeLayer, &scopeID)
	if err != nil {
		t.Fatalf("QueryRowContext() upgraded settings error = %v", err)
	}
	if scopeLayer != "daemon-global" || scopeID != "daemon-global" {
		t.Fatalf("upgraded settings scope = (%q, %q), want daemon-global defaults", scopeLayer, scopeID)
	}

	profile, err := db.GetSettingsProfile(ctx, settings.LayerDaemonGlobal, "")
	if err != nil {
		t.Fatalf("GetSettingsProfile() after upgrade error = %v", err)
	}
	if profile.ScopeID != settings.DaemonGlobalScopeID {
		t.Fatalf("GetSettingsProfile() scope id = %q, want %q", profile.ScopeID, settings.DaemonGlobalScopeID)
	}
}

func TestMigrateUpRejectsAmbiguousLegacySettingsRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "ambiguous-legacy-settings.db"),
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	legacyStatements := []string{
		`CREATE TABLE settings (
			id TEXT PRIMARY KEY,
			lock_version INTEGER NOT NULL CHECK (lock_version > 0),
			payload TEXT NOT NULL CHECK (json_valid(payload)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_settings_updated_at ON settings(updated_at)`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-1', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-2', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`CREATE TABLE schema_migrations (version uint64,dirty bool)`,
		`INSERT INTO schema_migrations(version, dirty) VALUES (1, 0)`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before MigrateUp error = %v", err)
	}

	if err := MigrateUp(ctx, cfg); !errors.Is(err, ErrAmbiguousSettings) {
		t.Fatalf("MigrateUp() error = %v, want %v", err, ErrAmbiguousSettings)
	}
}

func TestOpenAndMigrateRejectsAmbiguousLegacySettingsRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "ambiguous-open-and-migrate.db"),
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	legacyStatements := []string{
		`CREATE TABLE settings (
			id TEXT PRIMARY KEY,
			lock_version INTEGER NOT NULL CHECK (lock_version > 0),
			payload TEXT NOT NULL CHECK (json_valid(payload)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_settings_updated_at ON settings(updated_at)`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-1', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-2', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`CREATE TABLE schema_migrations (version uint64,dirty bool)`,
		`INSERT INTO schema_migrations(version, dirty) VALUES (1, 0)`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before OpenAndMigrate error = %v", err)
	}

	if _, err := OpenAndMigrate(ctx, cfg); !errors.Is(err, ErrAmbiguousSettings) {
		t.Fatalf("OpenAndMigrate() error = %v, want %v", err, ErrAmbiguousSettings)
	}
}

func TestMigrateDownAllowsAmbiguousLegacySettingsRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "ambiguous-legacy-down.db"),
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	legacyStatements := []string{
		`CREATE TABLE settings (
			id TEXT PRIMARY KEY,
			lock_version INTEGER NOT NULL CHECK (lock_version > 0),
			payload TEXT NOT NULL CHECK (json_valid(payload)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_settings_updated_at ON settings(updated_at)`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-1', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('legacy-2', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`CREATE TABLE schema_migrations (version uint64,dirty bool)`,
		`INSERT INTO schema_migrations(version, dirty) VALUES (1, 0)`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before MigrateDown error = %v", err)
	}

	if err := MigrateDown(ctx, cfg); err != nil {
		t.Fatalf("MigrateDown() error = %v", err)
	}

	db, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after down error = %v", err)
	}

	assertTableExists(t, db.DB(), "settings", false)
}

func TestMigrateDownAndUpPreservesScopedSettingsIDs(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "round-trip-settings.db"),
	}

	store, err := OpenAndMigrate(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}

	registry := settings.MustRegistry(
		settings.KeySpec{Key: "runner.image", AllowedLayers: []settings.Layer{settings.LayerProject, settings.LayerWorkflow}},
	)

	if _, err := store.CreateSettingsProfile(ctx, registry, settings.LayerProject, "project-1", map[string]string{
		"runner.image": "project-image",
	}); err != nil {
		t.Fatalf("CreateSettingsProfile() project error = %v", err)
	}
	if _, err := store.CreateSettingsProfile(ctx, registry, settings.LayerWorkflow, "workflow-1", map[string]string{
		"runner.image": "workflow-image",
	}); err != nil {
		t.Fatalf("CreateSettingsProfile() workflow error = %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close() before down error = %v", err)
	}

	if err := MigrateDownSteps(ctx, cfg, 1); err != nil {
		t.Fatalf("MigrateDownSteps() error = %v", err)
	}
	if err := MigrateUp(ctx, cfg); err != nil {
		t.Fatalf("MigrateUp() after down error = %v", err)
	}

	store, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after round-trip error = %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	projectProfile, err := store.GetSettingsProfile(ctx, settings.LayerProject, "project-1")
	if err != nil {
		t.Fatalf("GetSettingsProfile() project after round-trip error = %v", err)
	}
	if projectProfile.Values["runner.image"] != "project-image" {
		t.Fatalf("project runner.image = %q, want %q", projectProfile.Values["runner.image"], "project-image")
	}

	workflowProfile, err := store.GetSettingsProfile(ctx, settings.LayerWorkflow, "workflow-1")
	if err != nil {
		t.Fatalf("GetSettingsProfile() workflow after round-trip error = %v", err)
	}
	if workflowProfile.Values["runner.image"] != "workflow-image" {
		t.Fatalf("workflow runner.image = %q, want %q", workflowProfile.Values["runner.image"], "workflow-image")
	}
}

func TestMigrateUpTreatsMalformedScopedLegacyIDAsGenericLegacy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := Config{
		Path: filepath.Join(t.TempDir(), "malformed-legacy-settings.db"),
	}

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	legacyStatements := []string{
		`CREATE TABLE settings (
			id TEXT PRIMARY KEY,
			lock_version INTEGER NOT NULL CHECK (lock_version > 0),
			payload TEXT NOT NULL CHECK (json_valid(payload)),
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX idx_settings_updated_at ON settings(updated_at)`,
		`INSERT INTO settings (id, lock_version, payload, created_at, updated_at) VALUES ('project:', 1, '{}', '2026-05-01T00:00:00Z', '2026-05-01T00:00:00Z')`,
		`CREATE TABLE schema_migrations (version uint64,dirty bool)`,
		`INSERT INTO schema_migrations(version, dirty) VALUES (1, 0)`,
	}
	for _, statement := range legacyStatements {
		if _, err := db.DB().ExecContext(ctx, statement); err != nil {
			t.Fatalf("ExecContext(%q) error = %v", statement, err)
		}
	}

	if err := db.Close(); err != nil {
		t.Fatalf("Close() before MigrateUp error = %v", err)
	}

	if err := MigrateUp(ctx, cfg); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	db, err = Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() after upgrade error = %v", err)
	}

	var scopeLayer, scopeID string
	err = db.DB().QueryRowContext(ctx, `SELECT scope_layer, scope_id FROM settings WHERE id = 'daemon-global:daemon-global'`).Scan(&scopeLayer, &scopeID)
	if err != nil {
		t.Fatalf("QueryRowContext() migrated malformed id error = %v", err)
	}
	if scopeLayer != "daemon-global" || scopeID != "daemon-global" {
		t.Fatalf("malformed id migrated scope = (%q, %q), want daemon-global defaults", scopeLayer, scopeID)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatalf("query sqlite_master for %s: %v", table, err)
	}

	if got := count == 1; got != want {
		t.Fatalf("table %s exists = %t, want %t", table, got, want)
	}
}

func assertTableHasColumns(t *testing.T, db *sql.DB, table string, want bool, columns ...string) {
	t.Helper()

	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(%s) error = %v", table, err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && !errors.Is(closeErr, sql.ErrConnDone) {
			t.Fatalf("rows.Close() error = %v", closeErr)
		}
	}()

	found := make(map[string]bool, len(columns))
	for rows.Next() {
		var (
			cid        int
			name       string
			typ        string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultVal, &pk); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		found[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() error = %v", err)
	}

	for _, column := range columns {
		if got := found[column]; got != want {
			t.Fatalf("column %s on %s exists = %t, want %t", column, table, got, want)
		}
	}
}
