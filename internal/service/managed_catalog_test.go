package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/workflow"
)

func TestManagedCatalogRemoteAdapterReportsSourceRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	document := map[string]any{
		"name": "managed-catalog-fixture",
		"plugins": []any{
			map[string]any{
				"name":             "coding-claude-code",
				"source":           map[string]any{"source": "github", "repo": "earchibald/rein-adapter-claude-code", "ref": "main"},
				"version":          "0.1.0",
				"description":      "Claude Code coding-agent bootstrap",
				"category":         "codingAgent",
				"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
				"capabilities": map[string]string{
					"patch.apply":  "true",
					"pull_request": "true",
					"shell.exec":   "true",
				},
			},
		},
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json) error = %v", err)
	}

	registry, err := adapter.Load(root, adapter.DiscoveryOptions{AllowUnsignedIndex: true})
	if err != nil {
		t.Fatalf("adapter.Load() error = %v", err)
	}
	managedAdapter, ok := NewManagedCatalog(registry).Lookup("coding-claude-code")
	if !ok {
		t.Fatal(`Lookup("coding-claude-code") = !ok`)
	}

	err = managedAdapter.Run(context.Background(), &workflow.RuntimeState{}, workflow.Phase{}, workflow.Direction(""), nil)
	want := `adapter "coding-claude-code" is registered from GitHub repo "earchibald/rein-adapter-claude-code", but remote managed execution is not wired into the daemon yet`
	if err == nil || err.Error() != want {
		t.Fatalf("Run() error = %v, want %q", err, want)
	}
}
