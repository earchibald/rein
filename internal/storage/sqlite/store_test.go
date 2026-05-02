package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStoreCreateGetUpdateDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

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
	store := openTestStore(t, ctx)

	if _, err := store.Create(ctx, WorkflowKind, "workflow-1", json.RawMessage(`{`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("Create() invalid payload error = %v, want %v", err, ErrInvalidPayload)
	}
}

func TestOpenInMemoryAndMigrate(t *testing.T) {
	t.Parallel()

	store, err := OpenInMemoryAndMigrate(context.Background(), t.Name())
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
	}()

	assertTableExists(t, store.DB(), "projects", true)
}

func TestInMemoryConfigUsesSharedCacheURI(t *testing.T) {
	t.Parallel()

	cfg := InMemoryConfig("rein test")
	if !strings.Contains(cfg.Path, "mode=memory") || !strings.Contains(cfg.Path, "cache=shared") {
		t.Fatalf("InMemoryConfig() path = %q", cfg.Path)
	}
	if !isInMemoryPath(cfg.Path) {
		t.Fatalf("isInMemoryPath(%q) = false, want true", cfg.Path)
	}
}

func TestOpenRejectsMissingPath(t *testing.T) {
	t.Parallel()

	if _, err := Open(context.Background(), Config{}); !errors.Is(err, ErrMissingPath) {
		t.Fatalf("Open() error = %v, want %v", err, ErrMissingPath)
	}
}

func TestTableForUnknownKind(t *testing.T) {
	t.Parallel()

	if _, err := tableFor(EntityKind("nope")); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("tableFor() error = %v, want %v", err, ErrUnknownKind)
	}
}

func TestStoreGetRejectsEmptyID(t *testing.T) {
	t.Parallel()

	_, err := openTestStore(t, context.Background()).Get(context.Background(), ProjectKind, "")
	if !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Get() error = %v, want %v", err, ErrEmptyID)
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
