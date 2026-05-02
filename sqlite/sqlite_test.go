package sqlite

import (
	"context"
	"testing"
)

func TestOpenInMemoryAndMigrateCreatesUsableStore(t *testing.T) {
	t.Parallel()

	store, err := OpenInMemoryAndMigrate(context.Background(), "public-wrapper")
	if err != nil {
		t.Fatalf("OpenInMemoryAndMigrate() error = %v", err)
	}
	defer store.Close()

	record, err := store.Create(context.Background(), ProjectKind, "project-1", []byte(`{"id":"project-1","displayName":"Project 1"}`))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	loaded, err := store.Get(context.Background(), ProjectKind, "project-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if loaded.ID != record.ID {
		t.Fatalf("Get() id = %q, want %q", loaded.ID, record.ID)
	}
	if string(loaded.Payload) != string(record.Payload) {
		t.Fatalf("Get() payload = %s, want %s", loaded.Payload, record.Payload)
	}
}

func TestMigrateDownStepsRejectsInvalidStepCount(t *testing.T) {
	t.Parallel()

	if err := MigrateDownSteps(context.Background(), Config{Path: "rein.db"}, 0); err != ErrInvalidMigrationStep {
		t.Fatalf("MigrateDownSteps() error = %v, want %v", err, ErrInvalidMigrationStep)
	}
}
