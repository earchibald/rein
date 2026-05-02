package adapter

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

func TestLoadSignedMarketplace(t *testing.T) {
	t.Parallel()

	root := writeMarketplaceFixture(t, fixtureOptions{Signed: true})
	registry, err := Load(root, DiscoveryOptions{TrustedKeys: trustedKeys()})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got, want := adapterIDs(registry.List(reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED, false)), []string{"coding-local", "messaging-local", "mux-local", "projection-local", "tracker-remote"}; !slices.Equal(got, want) {
		t.Fatalf("List() ids = %v, want %v", got, want)
	}

	coding, ok := registry.Entry("coding-local")
	if !ok {
		t.Fatal("Entry(coding-local) = !ok")
	}
	if got := coding.Descriptor.GetVersion(); got != "1.2.3" {
		t.Fatalf("coding version = %q, want %q", got, "1.2.3")
	}
	if got := coding.Descriptor.GetCapabilities()["local.manifest"]; got != "true" {
		t.Fatalf("coding capabilities[local.manifest] = %q, want %q", got, "true")
	}

	messaging, ok := registry.Entry("messaging-local")
	if !ok {
		t.Fatal("Entry(messaging-local) = !ok")
	}
	if got := messaging.Descriptor.GetCategory(); got != reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION {
		t.Fatalf("messaging category = %s, want %s", got, reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION)
	}

	projection, ok := registry.Entry("projection-local")
	if !ok {
		t.Fatal("Entry(projection-local) = !ok")
	}
	if projection.MarketplaceCategory != CategoryProjection {
		t.Fatalf("projection marketplace category = %q, want %q", projection.MarketplaceCategory, CategoryProjection)
	}
	if got := projection.Descriptor.GetCategory(); got != reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT {
		t.Fatalf("projection proto category = %s, want %s", got, reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT)
	}
	if !projection.Tail {
		t.Fatal("projection tail = false, want true")
	}
	if got := projection.Descriptor.GetCapabilities()["requires"]; got != `["messaging.post"]` {
		t.Fatalf("projection requires capability = %q, want %q", got, `["messaging.post"]`)
	}

	tracker, ok := registry.Entry("tracker-remote")
	if !ok {
		t.Fatal("Entry(tracker-remote) = !ok")
	}
	if tracker.Source.Kind != SourceGitHub {
		t.Fatalf("tracker source kind = %q, want %q", tracker.Source.Kind, SourceGitHub)
	}
}

func TestLoadRejectsUnsignedMarketplaceByDefault(t *testing.T) {
	t.Parallel()

	root := writeMarketplaceFixture(t, fixtureOptions{})
	_, err := Load(root, DiscoveryOptions{TrustedKeys: trustedKeys()})
	if err == nil || err.Error() != "marketplace index signature is required" {
		t.Fatalf("Load() error = %v, want %q", err, "marketplace index signature is required")
	}
}

func TestLoadAllowsUnsignedMarketplaceWhenConfigured(t *testing.T) {
	t.Parallel()

	root := writeMarketplaceFixture(t, fixtureOptions{})
	registry, err := Load(root, DiscoveryOptions{
		AllowUnsignedIndex: true,
		TrustedKeys:        trustedKeys(),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got, want := len(registry.List(reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED, false)), 5; got != want {
		t.Fatalf("len(List()) = %d, want %d", got, want)
	}
}

func TestLoadRejectsDaemonAPIVersionMismatch(t *testing.T) {
	t.Parallel()

	root := writeMarketplaceFixture(t, fixtureOptions{Signed: true, MismatchDaemonVersion: true})
	_, err := Load(root, DiscoveryOptions{TrustedKeys: trustedKeys()})
	want := `load plugin "projection-local": daemonApiVersion "rein.v2" does not match daemon "rein.v1"`
	if err == nil || err.Error() != want {
		t.Fatalf("Load() error = %v, want %q", err, want)
	}
}

func TestNormalizeCategoryAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  Taxonomy
	}{
		{
			input: "coding_agent",
			want: Taxonomy{
				Marketplace: CategoryCodingAgent,
				Proto:       reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
				Alias:       "coding_agent",
			},
		},
		{
			input: "multiplexer",
			want: Taxonomy{
				Marketplace: CategoryMux,
				Proto:       reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER,
				Alias:       "multiplexer",
			},
		},
		{
			input: "notification",
			want: Taxonomy{
				Marketplace: CategoryMessaging,
				Proto:       reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION,
				Alias:       "notification",
			},
		},
		{
			input: "review_agent",
			want: Taxonomy{
				Marketplace: CategoryProjection,
				Proto:       reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT,
				Alias:       "review_agent",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeCategory(tt.input)
			if err != nil {
				t.Fatalf("NormalizeCategory() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeCategory() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func adapterIDs(adapters []*reinv1.Adapter) []string {
	ids := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.GetId())
	}
	return ids
}

type fixtureOptions struct {
	Signed                bool
	MismatchDaemonVersion bool
}

func writeMarketplaceFixture(t *testing.T, options fixtureOptions) string {
	t.Helper()

	root := t.TempDir()
	mustMkdirAll(t, filepath.Join(root, ".claude-plugin"))

	writePluginManifest(t, root, "coding-local", map[string]any{
		"name":             "coding-local",
		"version":          "1.2.3",
		"description":      "Local coding adapter manifest",
		"category":         "codingAgent",
		"daemonApiVersion": CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"patch.apply":    "true",
			"local.manifest": "true",
		},
	})
	writePluginManifest(t, root, "mux-local", map[string]any{
		"name":             "mux-local",
		"version":          "0.4.0",
		"description":      "Local mux adapter manifest",
		"category":         "mux",
		"daemonApiVersion": CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"session.attach": "true",
		},
	})
	writePluginManifest(t, root, "messaging-local", map[string]any{
		"name":             "messaging-local",
		"version":          "0.6.0",
		"description":      "Local messaging adapter manifest",
		"category":         "messaging",
		"daemonApiVersion": CurrentDaemonAPIVersion,
		"capabilities": map[string]string{
			"messaging.post": "true",
		},
	})
	writePluginManifest(t, root, "projection-local", map[string]any{
		"name":             "projection-local",
		"version":          "0.9.0",
		"description":      "Local projection adapter manifest",
		"category":         "projection",
		"daemonApiVersion": daemonVersion(options.MismatchDaemonVersion),
		"tail":             true,
		"requires":         []string{"messaging.post"},
		"capabilities": map[string]string{
			"projection.sync": "true",
		},
	})

	document := map[string]any{
		"name": "rein-fixture",
		"plugins": []any{
			map[string]any{
				"name":             "coding-local",
				"source":           "./plugins/coding-local",
				"version":          "0.1.0",
				"description":      "Marketplace fallback coding metadata",
				"category":         "codingAgent",
				"daemonApiVersion": CurrentDaemonAPIVersion,
				"capabilities": map[string]string{
					"patch.apply": "true",
				},
			},
			map[string]any{
				"name":             "mux-local",
				"source":           "./plugins/mux-local",
				"version":          "0.4.0",
				"description":      "Marketplace mux metadata",
				"category":         "mux",
				"daemonApiVersion": CurrentDaemonAPIVersion,
			},
			map[string]any{
				"name":             "tracker-remote",
				"source":           map[string]any{"source": "github", "repo": "example/tracker", "sha": "0123456789abcdef0123456789abcdef01234567"},
				"version":          "1.0.0",
				"description":      "Remote tracker metadata",
				"category":         "tracker",
				"daemonApiVersion": CurrentDaemonAPIVersion,
				"capabilities": map[string]string{
					"issue.sync": "true",
				},
			},
			map[string]any{
				"name":             "messaging-local",
				"source":           "./plugins/messaging-local",
				"version":          "0.6.0",
				"description":      "Marketplace messaging metadata",
				"category":         "messaging",
				"daemonApiVersion": CurrentDaemonAPIVersion,
			},
			map[string]any{
				"name":             "projection-local",
				"source":           "./plugins/projection-local",
				"version":          "0.9.0",
				"description":      "Marketplace projection metadata",
				"category":         "projection",
				"daemonApiVersion": daemonVersion(options.MismatchDaemonVersion),
				"tail":             true,
				"requires":         []string{"messaging.post"},
			},
		},
	}

	if options.Signed {
		canonical, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("json.Marshal() error = %v", err)
		}
		signature := ed25519.Sign(privateKey(), canonical)
		document["signature"] = map[string]any{
			"algorithm": "ed25519",
			"keyId":     "test-key",
			"value":     base64.StdEncoding.EncodeToString(signature),
		}
	}

	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(marketplace.json) error = %v", err)
	}

	return root
}

func writePluginManifest(t *testing.T, root, name string, manifest map[string]any) {
	t.Helper()

	manifestDir := filepath.Join(root, "plugins", name, ".claude-plugin")
	mustMkdirAll(t, manifestDir)

	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(plugin.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.json) error = %v", err)
	}
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func daemonVersion(mismatch bool) string {
	if mismatch {
		return "rein.v2"
	}
	return CurrentDaemonAPIVersion
}

func trustedKeys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{
		"test-key": privateKey().Public().(ed25519.PublicKey),
	}
}

func privateKey() ed25519.PrivateKey {
	seed := []byte("rein-rn11-marketplace-signing-se")
	return ed25519.NewKeyFromSeed(seed)
}
