package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestApplyDashboardsOutputsSkippedIDs(t *testing.T) {
	t.Parallel()

	root := writeDashboardsApplyFixture(t)
	var stored []map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/dashboards":
			type responseDashboard struct {
				ID   string         `json:"id"`
				Data map[string]any `json:"data"`
			}
			dashboards := make([]responseDashboard, 0, len(stored))
			for index, payload := range stored {
				dashboards = append(dashboards, responseDashboard{
					ID:   "created-1",
					Data: payload,
				})
				if index > 0 {
					t.Fatalf("unexpected stored dashboards: %d", len(stored))
				}
			}
			_ = json.NewEncoder(w).Encode(dashboards)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/dashboards":
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("Decode(POST body) error = %v", err)
			}
			stored = []map[string]any{payload}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "created-1", "data": payload})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	app := newApp(&stdout, &bytes.Buffer{}, func(string) (string, bool) { return "", false }, nil, nil)
	config := dashboardsApplyConfig{
		Plugin:   "rein-dashboards",
		BaseURL:  server.URL,
		APIKey:   "secret",
		RootPath: root,
	}

	if err := app.applyDashboards(config); err != nil {
		t.Fatalf("applyDashboards() first error = %v", err)
	}
	stdout.Reset()

	if err := app.applyDashboards(config); err != nil {
		t.Fatalf("applyDashboards() second error = %v", err)
	}

	var output dashboardsApplyOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.String())
	}
	if output.Repository != root {
		t.Fatalf("Repository = %q, want %q", output.Repository, root)
	}
	if got, want := output.SkippedIDs, []string{"created-1"}; !slices.Equal(got, want) {
		t.Fatalf("SkippedIDs = %v, want %v", got, want)
	}
	if len(output.CreatedIDs) != 0 || len(output.UpdatedIDs) != 0 {
		t.Fatalf("CreatedIDs/UpdatedIDs = %v/%v, want empty", output.CreatedIDs, output.UpdatedIDs)
	}
}

func writeDashboardsApplyFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude-plugin) error = %v", err)
	}
	writeDashboardsApplyJSONFile(t, filepath.Join(root, ".claude-plugin", "dashboards-marketplace.json"), map[string]any{
		"name": "rein-dashboards",
		"plugins": []any{
			map[string]any{
				"name":        "rein-dashboards",
				"version":     "0.1.0",
				"description": "Fixture dashboards plugin",
				"provider":    "signoz",
				"source":      "./plugins/rein-dashboards",
			},
		},
	})

	pluginRoot := filepath.Join(root, "plugins", "rein-dashboards", ".claude-plugin")
	if err := os.MkdirAll(pluginRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(pluginRoot) error = %v", err)
	}
	writeDashboardsApplyJSONFile(t, filepath.Join(pluginRoot, "plugin.json"), map[string]any{
		"name":        "rein-dashboards",
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
	writeDashboardsApplyJSONFile(t, filepath.Join(assetDir, "rein-daemon-otlp.json"), map[string]any{
		"title":   "Rein Daemon OTLP",
		"version": "v5",
		"widgets": []any{},
		"layout":  []any{},
	})
	return root
}

func writeDashboardsApplyJSONFile(t *testing.T, path string, value any) {
	t.Helper()

	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
