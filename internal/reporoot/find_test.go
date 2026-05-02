package reporoot

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindWalksUpToMarker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude-plugin) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json) error = %v", err)
	}

	start := filepath.Join(root, "nested", "deeper")
	got, err := Find(start, ".claude-plugin/marketplace.json", "adapter marketplace")
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if got != root {
		t.Fatalf("Find() root = %q, want %q", got, root)
	}
}

func TestFindReturnsHelpfulErrorWhenMarkerMissing(t *testing.T) {
	t.Parallel()

	start := filepath.Join(t.TempDir(), "nested", "deeper")
	_, err := Find(start, ".claude-plugin/marketplace.json", "adapter marketplace")
	want := `adapter marketplace ".claude-plugin/marketplace.json" was not found from ` + `"` + start + `"`
	if err == nil || err.Error() != want {
		t.Fatalf("Find() error = %v, want %q", err, want)
	}
}
