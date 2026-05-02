package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
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

func TestMigrateDownStepsDropsSchema(t *testing.T) {
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

	assertTableExists(t, store.DB(), "projects", false)

	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
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
