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
	return runMigrations(ctx, cfg, func(m *migrate.Migrate) error {
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func MigrateDown(ctx context.Context, cfg Config) error {
	return runMigrations(ctx, cfg, func(m *migrate.Migrate) error {
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

	return runMigrations(ctx, cfg, func(m *migrate.Migrate) error {
		if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return err
		}
		return nil
	})
}

func runMigrations(ctx context.Context, cfg Config, apply func(*migrate.Migrate) error) (err error) {
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
