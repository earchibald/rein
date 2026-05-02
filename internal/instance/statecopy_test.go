package instance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicCopyDirSkipsRuntimeArtifacts(t *testing.T) {
	t.Parallel()

	src := filepath.Join(t.TempDir(), "src")
	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "rein.db"), []byte("db"), 0o600); err != nil {
		t.Fatalf("WriteFile(db) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "notes.txt"), []byte("notes"), 0o644); err != nil {
		t.Fatalf("WriteFile(notes) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, pidFileName), []byte("123\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(pid) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, socketFileName), []byte("socket"), 0o600); err != nil {
		t.Fatalf("WriteFile(socket) error = %v", err)
	}

	dst := filepath.Join(t.TempDir(), "backup")
	if err := AtomicCopyDir(src, dst, CopyDirOptions{Filter: SkipRuntimeArtifacts}); err != nil {
		t.Fatalf("AtomicCopyDir() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "rein.db")); err != nil {
		t.Fatalf("copied database missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "nested", "notes.txt")); err != nil {
		t.Fatalf("copied nested file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, pidFileName)); !os.IsNotExist(err) {
		t.Fatalf("pid file stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(dst, socketFileName)); !os.IsNotExist(err) {
		t.Fatalf("socket stat error = %v, want not exist", err)
	}
}

func TestAtomicReplaceDirReplacesExistingState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "backup")
	dst := filepath.Join(root, "instance")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatalf("MkdirAll(dst) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "rein.db"), []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(new) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dst, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	if err := AtomicReplaceDir(src, dst, CopyDirOptions{Filter: SkipRuntimeArtifacts}); err != nil {
		t.Fatalf("AtomicReplaceDir() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "rein.db")); err != nil {
		t.Fatalf("restored database missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old state stat error = %v, want not exist", err)
	}
}
