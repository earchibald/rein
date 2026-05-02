package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreCreateGetUpdateDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenAndMigrate(ctx, Config{
		Path: filepath.Join(t.TempDir(), "rein.db"),
	})
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	created, err := store.Create(ctx, ProjectKind, "project-1", json.RawMessage(`{"name":"rein"}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.LockVersion != 1 {
		t.Fatalf("Create() lock version = %d, want 1", created.LockVersion)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Create() timestamps were not set: %+v", created)
	}

	got, err := store.Get(ctx, ProjectKind, "project-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("Get() id = %q, want %q", got.ID, created.ID)
	}
	if string(got.Payload) != `{"name":"rein"}` {
		t.Fatalf("Get() payload = %s", string(got.Payload))
	}

	if _, err := store.Update(ctx, ProjectKind, "project-1", 99, json.RawMessage(`{"name":"next"}`)); !errors.Is(err, ErrLockVersionMismatch) {
		t.Fatalf("Update() stale lock error = %v, want %v", err, ErrLockVersionMismatch)
	}

	updated, err := store.Update(ctx, ProjectKind, "project-1", created.LockVersion, json.RawMessage(`{"name":"next"}`))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.LockVersion != 2 {
		t.Fatalf("Update() lock version = %d, want 2", updated.LockVersion)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && !updated.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("Update() updated_at = %s, want >= %s", updated.UpdatedAt, created.UpdatedAt)
	}

	if err := store.Delete(ctx, ProjectKind, "project-1", created.LockVersion); !errors.Is(err, ErrLockVersionMismatch) {
		t.Fatalf("Delete() stale lock error = %v, want %v", err, ErrLockVersionMismatch)
	}

	if err := store.Delete(ctx, ProjectKind, "project-1", updated.LockVersion); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := store.Get(ctx, ProjectKind, "project-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want %v", err, ErrNotFound)
	}
}

func TestStoreRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store, err := OpenAndMigrate(ctx, Config{
		Path: filepath.Join(t.TempDir(), "rein.db"),
	})
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	if _, err := store.Create(ctx, WorkflowKind, "workflow-1", json.RawMessage(`{`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Create() invalid payload error = %v, want %v", err, ErrInvalidPayload)
	}
}

func TestConfigNormalizeSetsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := (Config{Path: "rein.db"}).normalize()
	if err != nil {
		t.Fatalf("normalize() error = %v", err)
	}
	if cfg.MigrationsTable != defaultMigrationsTable {
		t.Fatalf("normalize() migrations table = %q, want %q", cfg.MigrationsTable, defaultMigrationsTable)
	}
	if cfg.BusyTimeout != defaultBusyTimeout {
		t.Fatalf("normalize() busy timeout = %s, want %s", cfg.BusyTimeout, defaultBusyTimeout)
	}

	if _, err := (Config{}).normalize(); !errors.Is(err, ErrMissingPath) {
		t.Fatalf("normalize() missing path error = %v, want %v", err, ErrMissingPath)
	}
}

func TestClonePayloadReturnsDistinctSlice(t *testing.T) {
	t.Parallel()

	original := json.RawMessage(`{"ok":true}`)
	cloned := clonePayload(original)
	original[0] = '['

	if string(cloned) != `{"ok":true}` {
		t.Fatalf("clonePayload() = %s", string(cloned))
	}
}
