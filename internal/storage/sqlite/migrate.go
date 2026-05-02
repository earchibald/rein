package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func MigrateUp(ctx context.Context, cfg Config) error {
	return runMigrations(ctx, cfg, true, func(m *migrate.Migrate) error {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func MigrateDown(ctx context.Context, cfg Config) error {
	return runMigrations(ctx, cfg, false, func(m *migrate.Migrate) error {
		if err := m.Down(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func MigrateDownSteps(ctx context.Context, cfg Config, steps int) error {
	if steps <= 0 {
		return ErrInvalidMigrationStep
	}

	return runMigrations(ctx, cfg, false, func(m *migrate.Migrate) error {
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func runMigrations(ctx context.Context, cfg Config, validateLegacyUpgrade bool, apply func(*migrate.Migrate) error) (err error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return err
	}

	db, err := openDB(ctx, normalized)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}()

	if validateLegacyUpgrade {
		if err := validateLegacySettingsUpgrade(ctx, db, normalized.MigrationsTable); err != nil {
			return err
		}
	}

	migrator, err := newMigrator(normalized, db)
	if err != nil {
		return err
	}
	defer func() {
		sourceErr, closeErr := migrator.Close()
		err = errors.Join(err, sourceErr, closeErr)
	}()

	return applyMigrator(migrator, apply)
}

func validateLegacySettingsUpgrade(ctx context.Context, db *sql.DB, migrationsTable string) error {
	migrationsTableExists, err := sqliteTableExists(ctx, db, migrationsTable)
	if err != nil || !migrationsTableExists {
		return err
	}

	var (
		version int64
		dirty   bool
	)
	err = db.QueryRowContext(ctx, fmt.Sprintf(`SELECT version, dirty FROM %s LIMIT 1`, migrationsTable)).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("sqlite: inspect migration state: %w", err)
	}
	if dirty || version != 1 {
		return nil
	}

	scopeColumnExists, err := sqliteColumnExists(ctx, db, "settings", "scope_layer")
	if err != nil || scopeColumnExists {
		return err
	}

	var settingsCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM settings`).Scan(&settingsCount); err != nil {
		return fmt.Errorf("sqlite: inspect legacy settings rows: %w", err)
	}
	if settingsCount <= 1 {
		return nil
	}

	var canonicalCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM settings
		WHERE id = 'daemon-global:daemon-global'
			OR (id LIKE 'project:%' AND length(id) > length('project:'))
			OR (id LIKE 'workflow:%' AND length(id) > length('workflow:'))
			OR (id LIKE 'execution:%' AND length(id) > length('execution:'))
	`).Scan(&canonicalCount); err != nil {
		return fmt.Errorf("sqlite: inspect canonical legacy settings ids: %w", err)
	}
	if canonicalCount != settingsCount {
		return ErrAmbiguousSettings
	}

	return nil
}

func sqliteTableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		return false, fmt.Errorf("sqlite: inspect table %q: %w", table, err)
	}

	return count == 1, nil
}

func sqliteColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("sqlite: inspect columns for %q: %w", table, err)
	}
	defer rows.Close()

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
			return false, fmt.Errorf("sqlite: scan columns for %q: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("sqlite: iterate columns for %q: %w", table, err)
	}

	return false, nil
}

func newMigrator(cfg Config, db *sql.DB) (*migrate.Migrate, error) {
	sourceDriver, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("sqlite: init migration source: %w", err)
	}

	dbDriver, err := migratesqlite.WithInstance(db, &migratesqlite.Config{
		MigrationsTable: cfg.MigrationsTable,
		DatabaseName:    cfg.Path,
	})
	if err != nil {
		_ = sourceDriver.Close()
		return nil, fmt.Errorf("sqlite: init migration database driver: %w", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, driverName, dbDriver)
	if err != nil {
		_ = sourceDriver.Close()
		_ = dbDriver.Close()
		return nil, fmt.Errorf("sqlite: init migrator: %w", err)
	}

	return migrator, nil
}

func applyMigrator(migrator *migrate.Migrate, apply func(*migrate.Migrate) error) error {
	if err := apply(migrator); err != nil {
		return fmt.Errorf("sqlite: apply migrations: %w", err)
	}

	return nil
}
