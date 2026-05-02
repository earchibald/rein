package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointWALTruncatesLiveDatabase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "rein.db")
	store, err := OpenAndMigrate(ctx, Config{Path: dbPath})
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer store.Close()

	for i := 0; i < 8; i++ {
		payload, _ := json.Marshal(map[string]any{"index": i})
		if _, err := store.Create(ctx, ProjectKind, fmt.Sprintf("project-%d", i), payload); err != nil {
			t.Fatalf("Create(%d) error = %v", i, err)
		}
	}

	walPath := dbPath + "-wal"
	if info, err := os.Stat(walPath); err != nil {
		t.Fatalf("os.Stat(%q) error = %v", walPath, err)
	} else if info.Size() == 0 {
		t.Fatalf("wal size = 0, want pending frames before checkpoint")
	}

	result, err := CheckpointWAL(ctx, Config{Path: dbPath})
	if err != nil {
		t.Fatalf("CheckpointWAL() error = %v", err)
	}
	if result.Busy != 0 {
		t.Fatalf("CheckpointWAL() busy = %d, want 0", result.Busy)
	}
	if info, err := os.Stat(walPath); err != nil {
		t.Fatalf("os.Stat(%q) after checkpoint error = %v", walPath, err)
	} else if info.Size() != 0 {
		t.Fatalf("wal size after checkpoint = %d, want 0", info.Size())
	}
}
