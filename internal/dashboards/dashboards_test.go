package dashboards

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestLoadLocalMarketplacePlugin(t *testing.T) {
	t.Parallel()

	root := writeDashboardFixture(t, fixtureOptions{})
	registry, err := Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	entry, ok := registry.Entry(DefaultPluginName)
	if !ok {
		t.Fatalf("Entry(%q) = !ok", DefaultPluginName)
	}
	if entry.Manifest.Provider != "signoz" {
		t.Fatalf("Manifest.Provider = %q, want signoz", entry.Manifest.Provider)
	}
	if len(entry.Manifest.Assets) != 1 || entry.Manifest.Assets[0].ID != "rein-daemon-otlp" {
		t.Fatalf("Manifest.Assets = %+v", entry.Manifest.Assets)
	}
}

func TestApplyCreatesAndUpdatesDashboards(t *testing.T) {
	t.Parallel()

	root := writeDashboardFixture(t, fixtureOptions{})
	var stored []sigNozDashboard

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(sigNozAPIKeyHeader); got != "secret" {
			t.Fatalf("%s = %q, want secret", sigNozAPIKeyHeader, got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dashboards":
			_ = json.NewEncoder(w).Encode(stored)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/dashboards":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode(POST body) error = %v", err)
			}
			dashboard := sigNozDashboard{ID: "created-1", Data: payload}
			stored = append(stored, dashboard)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(dashboard)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/dashboards/created-1":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode(PUT body) error = %v", err)
			}
			stored = []sigNozDashboard{{ID: "created-1", Data: payload}}
			_ = json.NewEncoder(w).Encode(stored[0])
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	first, err := Apply(context.Background(), root, ApplyOptions{
		Plugin:  DefaultPluginName,
		BaseURL: server.URL,
		APIKey:  "secret",
	})
	if err != nil {
		t.Fatalf("Apply() first error = %v", err)
	}
	if got, want := first.Created, []string{"created-1"}; !slices.Equal(got, want) {
		t.Fatalf("first.Created = %v, want %v", got, want)
	}
	if len(stored) != 1 || !hasTag(stored[0].Data["tags"], "rein-dashboards:rein-daemon-otlp") {
		t.Fatalf("stored dashboard tags = %+v", stored)
	}

	second, err := Apply(context.Background(), root, ApplyOptions{
		Plugin:  DefaultPluginName,
		BaseURL: server.URL,
		APIKey:  "secret",
	})
	if err != nil {
		t.Fatalf("Apply() second error = %v", err)
	}
	if got, want := second.Updated, []string{"created-1"}; !slices.Equal(got, want) {
		t.Fatalf("second.Updated = %v, want %v", got, want)
	}
}

func TestApplyRemotePluginReturnsExplicitError(t *testing.T) {
	t.Parallel()

	root := writeDashboardFixture(t, fixtureOptions{RemoteRepo: "earchibald/rein-dashboards"})
	_, err := Apply(context.Background(), root, ApplyOptions{
		Plugin:  DefaultPluginName,
		BaseURL: "https://signoz.example",
		APIKey:  "secret",
	})
	want := `dashboards plugin "rein-dashboards" is registered from GitHub repo "earchibald/rein-dashboards", but remote dashboards bootstrap is not wired into the CLI yet`
	if err == nil || err.Error() != want {
		t.Fatalf("Apply() error = %v, want %q", err, want)
	}
}

type fixtureOptions struct {
	RemoteRepo string
}

func writeDashboardFixture(t *testing.T, options fixtureOptions) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude-plugin) error = %v", err)
	}

	document := map[string]any{
		"name": "rein-dashboards",
		"plugins": []any{
			map[string]any{
				"name":        DefaultPluginName,
				"version":     "0.1.0",
				"description": "Fixture dashboards plugin",
				"provider":    "signoz",
				"source":      "./plugins/rein-dashboards",
			},
		},
	}
	if options.RemoteRepo != "" {
		document["plugins"] = []any{
			map[string]any{
				"name":        DefaultPluginName,
				"version":     "0.1.0",
				"description": "Fixture dashboards plugin",
				"provider":    "signoz",
				"source": map[string]any{
					"source": "github",
					"repo":   options.RemoteRepo,
					"ref":    "main",
				},
			},
		}
	}
	writeJSONFile(t, filepath.Join(root, ".claude-plugin", "dashboards-marketplace.json"), document)

	if options.RemoteRepo != "" {
		return root
	}

	pluginRoot := filepath.Join(root, "plugins", "rein-dashboards", ".claude-plugin")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(pluginRoot) error = %v", err)
	}
	writeJSONFile(t, filepath.Join(pluginRoot, "plugin.json"), map[string]any{
		"name":        DefaultPluginName,
		"version":     "0.1.0",
		"description": "Fixture dashboards plugin",
		"provider":    "signoz",
		"dashboards": []any{
			map[string]any{
				"id":    "rein-daemon-otlp",
				"title": "Rein Daemon OTLP",
				"path":  "signoz/rein-daemon-otlp.json",
			},
		},
	})
	assetDir := filepath.Join(root, "plugins", "rein-dashboards", "signoz")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetDir) error = %v", err)
	}
	writeJSONFile(t, filepath.Join(assetDir, "rein-daemon-otlp.json"), map[string]any{
		"title":   "Rein Daemon OTLP",
		"version": "v5",
		"widgets": []any{},
		"layout":  []any{},
	})
	return root
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()

	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
