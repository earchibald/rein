package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
)

type MigrationDiagnostic struct {
	Path            string `json:"path"`
	Exists          bool   `json:"exists"`
	Ready           bool   `json:"ready"`
	CurrentVersion  uint   `json:"currentVersion"`
	LatestVersion   uint   `json:"latestVersion"`
	Dirty           bool   `json:"dirty"`
	Outstanding     []uint `json:"outstanding"`
	Blocked         bool   `json:"blocked"`
	BlockedReason   string `json:"blockedReason,omitempty"`
	InspectionError string `json:"inspectionError,omitempty"`
	MigrationsTable string `json:"migrationsTable"`
}

func DiagnoseMigrations(ctx context.Context, cfg Config) MigrationDiagnostic {
	normalized, err := cfg.normalize()
	if err != nil {
		return MigrationDiagnostic{
			Path:            cfg.Path,
			InspectionError: err.Error(),
		}
	}

	versions := embeddedMigrationVersions()
	diagnostic := MigrationDiagnostic{
		Path:            normalized.Path,
		MigrationsTable: normalized.MigrationsTable,
		LatestVersion:   latestMigrationVersion(versions),
	}

	info, err := os.Stat(normalized.Path)
	if err != nil {
		if os.IsNotExist(err) {
			diagnostic.Outstanding = append([]uint(nil), versions...)
			return diagnostic
		}
		diagnostic.InspectionError = err.Error()
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}
	if info.IsDir() {
		diagnostic.Exists = true
		diagnostic.InspectionError = fmt.Sprintf("sqlite path %q is a directory", normalized.Path)
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}
	diagnostic.Exists = true

	db, err := openDB(ctx, normalized)
	if err != nil {
		diagnostic.InspectionError = err.Error()
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}
	defer db.Close()

	if err := validateLegacySettingsUpgrade(ctx, db, normalized.MigrationsTable); err != nil {
		diagnostic.Blocked = true
		diagnostic.BlockedReason = err.Error()
	}

	tableExists, err := sqliteTableExists(ctx, db, normalized.MigrationsTable)
	if err != nil {
		diagnostic.InspectionError = err.Error()
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}
	if !tableExists {
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}

	version, dirty, err := readMigrationVersion(ctx, db, normalized.MigrationsTable)
	if err != nil {
		diagnostic.InspectionError = err.Error()
		diagnostic.Outstanding = append([]uint(nil), versions...)
		return diagnostic
	}
	diagnostic.CurrentVersion = version
	diagnostic.Dirty = dirty
	diagnostic.Outstanding = outstandingMigrations(versions, version, dirty)
	diagnostic.Ready = !diagnostic.Blocked && diagnostic.InspectionError == "" && !diagnostic.Dirty && len(diagnostic.Outstanding) == 0

	return diagnostic
}

func readMigrationVersion(ctx context.Context, db *sql.DB, migrationsTable string) (uint, bool, error) {
	var (
		version uint
		dirty   bool
	)

	err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT version, dirty FROM %s LIMIT 1`, migrationsTable)).Scan(&version, &dirty)
	if err == nil {
		return version, dirty, nil
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return 0, false, fmt.Errorf("sqlite: inspect migration state: %w", err)
}

func embeddedMigrationVersions() []uint {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil
	}

	versions := make([]uint, 0, len(entries))
	seen := map[uint]struct{}{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			continue
		}
		value, err := strconv.ParseUint(prefix, 10, 64)
		if err != nil {
			continue
		}
		version := uint(value)
		if _, exists := seen[version]; exists {
			continue
		}
		seen[version] = struct{}{}
		versions = append(versions, version)
	}
	slices.Sort(versions)
	return versions
}

func latestMigrationVersion(versions []uint) uint {
	if len(versions) == 0 {
		return 0
	}
	return versions[len(versions)-1]
}

func outstandingMigrations(versions []uint, current uint, dirty bool) []uint {
	outstanding := make([]uint, 0, len(versions))
	for _, version := range versions {
		if dirty {
			if version >= current {
				outstanding = append(outstanding, version)
			}
			continue
		}
		if version > current {
			outstanding = append(outstanding, version)
		}
	}
	return outstanding
}
