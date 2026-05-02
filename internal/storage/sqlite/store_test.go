package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/earchibald/rein/internal/settings"
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

func TestStoreListByJSONField(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)

	records := map[string]json.RawMessage{
		"sideeffect-1": json.RawMessage(`{"execution_id":"exec-1","status":"pending"}`),
		"sideeffect-2": json.RawMessage(`{"execution_id":"exec-1","status":"applied"}`),
		"sideeffect-3": json.RawMessage(`{"execution_id":"exec-2","status":"pending"}`),
	}
	for id, payload := range records {
		if _, err := store.Create(ctx, SideEffectKind, id, payload); err != nil {
			t.Fatalf("Create(%q) error = %v", id, err)
		}
	}

	got, err := store.List(ctx, SideEffectKind, ListOptions{
		JSONEquals: map[string]string{
			"execution_id": "exec-1",
		},
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() len = %d, want 2", len(got))
	}

	limited, err := store.List(ctx, SideEffectKind, ListOptions{
		JSONEquals: map[string]string{
			"status": "pending",
		},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("List() with limit error = %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("List() with limit len = %d, want 1", len(limited))
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

func TestStoreSettingsProfilesCRUDAndResolve(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	registry := settings.MustRegistry(
		settings.KeySpec{Key: "runner.image", AllowedLayers: []settings.Layer{settings.LayerDaemonGlobal, settings.LayerProject, settings.LayerWorkflow, settings.LayerExecution}},
		settings.KeySpec{Key: "notifications.email", AllowedLayers: []settings.Layer{settings.LayerDaemonGlobal, settings.LayerProject}},
	)

	daemonProfile, err := store.CreateSettingsProfile(ctx, registry, settings.LayerDaemonGlobal, "", map[string]string{
		"runner.image":        "ubuntu-latest",
		"notifications.email": "ops@example.com",
	})
	if err != nil {
		t.Fatalf("CreateSettingsProfile() daemon error = %v", err)
	}

	projectProfile, err := store.CreateSettingsProfile(ctx, registry, settings.LayerProject, "project-1", map[string]string{
		"notifications.email": "project@example.com",
	})
	if err != nil {
		t.Fatalf("CreateSettingsProfile() project error = %v", err)
	}

	if _, err := store.CreateSettingsProfile(ctx, registry, settings.LayerWorkflow, "workflow-1", map[string]string{
		"runner.image": "workflow-image",
	}); err != nil {
		t.Fatalf("CreateSettingsProfile() workflow error = %v", err)
	}

	executionProfile, err := store.CreateSettingsProfile(ctx, registry, settings.LayerExecution, "execution-1", map[string]string{
		"runner.image": "execution-image",
	})
	if err != nil {
		t.Fatalf("CreateSettingsProfile() execution error = %v", err)
	}

	gotProject, err := store.GetSettingsProfile(ctx, settings.LayerProject, "project-1")
	if err != nil {
		t.Fatalf("GetSettingsProfile() error = %v", err)
	}
	if gotProject.ScopeID != "project-1" || gotProject.Values["notifications.email"] != "project@example.com" {
		t.Fatalf("GetSettingsProfile() = %+v", gotProject)
	}

	updatedProject, err := store.UpdateSettingsProfile(ctx, registry, settings.LayerProject, "project-1", projectProfile.LockVersion, map[string]string{
		"notifications.email": "alerts@example.com",
	})
	if err != nil {
		t.Fatalf("UpdateSettingsProfile() error = %v", err)
	}
	if updatedProject.LockVersion != projectProfile.LockVersion+1 {
		t.Fatalf("UpdateSettingsProfile() lock version = %d, want %d", updatedProject.LockVersion, projectProfile.LockVersion+1)
	}

	resolved, err := store.ResolveSettings(ctx, registry, SettingsResolutionScope{
		ProjectID:   "project-1",
		WorkflowID:  "workflow-1",
		ExecutionID: "execution-1",
	})
	if err != nil {
		t.Fatalf("ResolveSettings() error = %v", err)
	}
	if got := resolved["runner.image"]; got.Value != "execution-image" || got.Layer != settings.LayerExecution {
		t.Fatalf("ResolveSettings() runner.image = %+v, want execution override", got)
	}
	if got := resolved["notifications.email"]; got.Value != "alerts@example.com" || got.Layer != settings.LayerProject {
		t.Fatalf("ResolveSettings() notifications.email = %+v, want project override", got)
	}

	if err := store.DeleteSettingsProfile(ctx, settings.LayerExecution, "execution-1", executionProfile.LockVersion); err != nil {
		t.Fatalf("DeleteSettingsProfile() error = %v", err)
	}

	fallbackResolved, err := store.ResolveSettings(ctx, registry, SettingsResolutionScope{
		ProjectID:   "project-1",
		WorkflowID:  "workflow-1",
		ExecutionID: "execution-1",
	})
	if err != nil {
		t.Fatalf("ResolveSettings() after delete error = %v", err)
	}
	if got := fallbackResolved["runner.image"]; got.Value != "workflow-image" || got.Layer != settings.LayerWorkflow {
		t.Fatalf("ResolveSettings() fallback runner.image = %+v, want workflow override", got)
	}

	if daemonProfile.ScopeID != settings.DaemonGlobalScopeID {
		t.Fatalf("CreateSettingsProfile() daemon scope id = %q, want %q", daemonProfile.ScopeID, settings.DaemonGlobalScopeID)
	}
}

func TestStoreSettingsProfileRejectsIneligibleLayer(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	registry := settings.MustRegistry(
		settings.KeySpec{Key: "notifications.email", AllowedLayers: []settings.Layer{settings.LayerDaemonGlobal, settings.LayerProject}},
	)

	_, err := store.CreateSettingsProfile(ctx, registry, settings.LayerExecution, "execution-1", map[string]string{
		"notifications.email": "execution@example.com",
	})
	if !errors.Is(err, settings.ErrLayerNotAllowed) {
		t.Fatalf("CreateSettingsProfile() error = %v, want %v", err, settings.ErrLayerNotAllowed)
	}
}

func TestStoreSettingsProfileRejectsInvalidScopeID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t, ctx)
	registry := settings.MustRegistry(
		settings.KeySpec{Key: "runner.image", AllowedLayers: []settings.Layer{settings.LayerProject}},
	)

	_, err := store.CreateSettingsProfile(ctx, registry, settings.LayerProject, "", map[string]string{
		"runner.image": "project-image",
	})
	if !errors.Is(err, settings.ErrInvalidScopeID) {
		t.Fatalf("CreateSettingsProfile() error = %v, want %v", err, settings.ErrInvalidScopeID)
	}
}
