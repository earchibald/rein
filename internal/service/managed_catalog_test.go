package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
	muxadapter "github.com/earchibald/rein/internal/adapter/muxiterm"
	"github.com/earchibald/rein/internal/workflow"
)

func TestNewManagedCatalogUsesBuiltInMuxitermAdapter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManagedCatalogManifest(t, root, muxadapter.AdapterID, map[string]any{
		"name":             muxadapter.AdapterID,
		"version":          "0.9.0",
		"description":      "Bundled muxiterm adapter",
		"category":         "mux",
		"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"session.attach": "true",
			"pane.split":     "true",
			"input.send":     "true",
			"tail":           "true",
		},
	})
	writeManagedCatalogMarketplace(t, root, map[string]any{
		"name": "rein-fixture",
		"plugins": []any{
			map[string]any{
				"name":             muxadapter.AdapterID,
				"source":           "./plugins/muxiterm",
				"version":          "0.9.0",
				"description":      "Bundled muxiterm adapter",
				"category":         "mux",
				"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
			},
		},
	})

	registry, err := adapter.Load(root, adapter.DiscoveryOptions{AllowUnsignedIndex: true})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	catalog := NewManagedCatalog(registry)
	managed, ok := catalog.Lookup(muxadapter.AdapterID)
	if !ok {
		t.Fatalf("Lookup(%q) = !ok", muxadapter.AdapterID)
	}
	if _, ok := managed.(*muxadapter.Adapter); !ok {
		t.Fatalf("Lookup(%q) type = %T, want *muxiterm.Adapter", muxadapter.AdapterID, managed)
	}
	if got := managed.Descriptor().GetCategory(); got != reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER {
		t.Fatalf("Descriptor().Category = %s, want %s", got, reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER)
	}

	err = managed.Run(context.Background(), nil, workflow.Phase{ID: "mux-step"}, workflow.DirectionForward, &workflow.SideEffect{})
	if err == nil || !strings.Contains(err.Error(), "muxiterm operation is required") {
		t.Fatalf("Run() error = %v, want muxiterm operation error", err)
	}

	if got, want := managedCatalogIDs(catalog.List()), []string{muxadapter.AdapterID}; !slices.Equal(got, want) {
		t.Fatalf("List() ids = %v, want %v", got, want)
	}
}

func TestManagedWorkflowValidateAcceptsBuiltInMuxiterm(t *testing.T) {
	t.Parallel()

	server := &ManagedWorkflowServer{
		catalog: NewManagedCatalog(nil),
		engine:  workflow.New(nil),
	}
	resp, err := server.ValidateWorkflow(context.Background(), &reinv1.ValidateWorkflowRequest{
		Workflow: &reinv1.Workflow{
			Id:   "wf-mux",
			Name: "Mux workflow",
			Steps: []*reinv1.WorkflowStep{{
				Id:        "mux-step",
				Name:      "Probe mux",
				AdapterId: muxadapter.AdapterID,
				Inputs: map[string]string{
					workflow.InputOperation: "doctor",
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ValidateWorkflow() error = %v", err)
	}
	if !resp.GetValid() {
		t.Fatalf("ValidateWorkflow() valid = false, messages = %+v", resp.GetMessages())
	}
}

func TestManagedCatalogRemoteAdapterReportsSourceRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManagedCatalogMarketplace(t, root, map[string]any{
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
	})

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

func TestManagedCatalogWrapsBuiltInGitHubTracker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeManagedCatalogMarketplace(t, root, map[string]any{
		"name": "rein-fixture",
		"plugins": []any{
			map[string]any{
				"name":             githubTrackerAdapterID,
				"source":           "./plugins/" + githubTrackerAdapterID,
				"version":          "0.1.0",
				"description":      "Fixture GitHub tracker adapter",
				"category":         "tracker",
				"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
				"capabilities": map[string]string{
					"issue.sync":               "true",
					"branch.prepare":           "true",
					"worktree.create":          "true",
					"pull_request":             "true",
					"pull_request_review.poll": "true",
					"merge":                    "true",
				},
			},
		},
	})
	writeManagedCatalogManifest(t, root, githubTrackerAdapterID, map[string]any{
		"name":             githubTrackerAdapterID,
		"version":          "0.1.0",
		"description":      "Fixture GitHub tracker adapter",
		"category":         "tracker",
		"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"issue.sync":               "true",
			"branch.prepare":           "true",
			"worktree.create":          "true",
			"pull_request":             "true",
			"pull_request_review.poll": "true",
			"merge":                    "true",
		},
	})

	catalog, err := NewManagedCatalogFromRoot(root, adapter.DiscoveryOptions{AllowUnsignedIndex: true})
	if err != nil {
		t.Fatalf("NewManagedCatalogFromRoot() error = %v", err)
	}

	managed, ok := catalog.Lookup(githubTrackerAdapterID)
	if !ok {
		t.Fatalf("Lookup(%q) = !ok", githubTrackerAdapterID)
	}
	if _, ok := managed.(*githubTrackerAdapter); !ok {
		t.Fatalf("Lookup(%q) type = %T, want *githubTrackerAdapter", githubTrackerAdapterID, managed)
	}

	if got, want := managedCatalogIDs(catalog.List()), []string{muxadapter.AdapterID, githubTrackerAdapterID}; !slices.Equal(got, want) {
		t.Fatalf("List() ids = %v, want %v", got, want)
	}
}

func managedCatalogIDs(adapters []*reinv1.Adapter) []string {
	ids := make([]string, 0, len(adapters))
	for _, descriptor := range adapters {
		ids = append(ids, descriptor.GetId())
	}
	return ids
}

func writeManagedCatalogMarketplace(t *testing.T, root string, document map[string]any) {
	t.Helper()

	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(marketplace.json) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude-plugin) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json) error = %v", err)
	}
}

func writeManagedCatalogManifest(t *testing.T, root, name string, manifest map[string]any) {
	t.Helper()

	manifestDir := filepath.Join(root, "plugins", name, ".claude-plugin")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", manifestDir, err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(plugin.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.json) error = %v", err)
	}
}
