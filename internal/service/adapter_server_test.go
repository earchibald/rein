package service

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
)

func TestAdapterServerListGetValidate(t *testing.T) {
	t.Parallel()

	root := writeServiceFixture(t, false)
	server, err := NewAdapterServerFromRoot(root, adapter.DiscoveryOptions{TrustedKeys: serviceTrustedKeys()})
	if err != nil {
		t.Fatalf("NewAdapterServerFromRoot() error = %v", err)
	}

	listResp, err := server.ListAdapters(context.Background(), &reinv1.ListAdaptersRequest{
		Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION,
	})
	if err != nil {
		t.Fatalf("ListAdapters() error = %v", err)
	}
	if got, want := serviceAdapterIDs(listResp.GetAdapters()), []string{"messaging-local"}; !slices.Equal(got, want) {
		t.Fatalf("ListAdapters() ids = %v, want %v", got, want)
	}

	getResp, err := server.GetAdapter(context.Background(), &reinv1.GetAdapterRequest{Id: "projection-local"})
	if err != nil {
		t.Fatalf("GetAdapter() error = %v", err)
	}
	if got := getResp.GetAdapter().GetCategory(); got != reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT {
		t.Fatalf("GetAdapter() category = %s, want %s", got, reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT)
	}

	validateResp, err := server.ValidateAdapter(context.Background(), &reinv1.ValidateAdapterRequest{
		Adapter: &reinv1.Adapter{
			Id:       "broken",
			Name:     "broken",
			Category: reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION,
			Version:  "1.0.0",
			Capabilities: map[string]string{
				"tail": "definitely",
			},
		},
	})
	if err != nil {
		t.Fatalf("ValidateAdapter() error = %v", err)
	}
	if validateResp.GetValid() {
		t.Fatal("ValidateAdapter() valid = true, want false")
	}
	if got, want := validateResp.GetMessages()[0].GetField(), "adapter.capabilities.tail"; got != want {
		t.Fatalf("ValidateAdapter() first field = %q, want %q", got, want)
	}
}

func TestAdapterServerPropagatesRegistryLoadFailure(t *testing.T) {
	t.Parallel()

	root := writeServiceFixture(t, true)
	server, err := NewAdapterServerFromRoot(root, adapter.DiscoveryOptions{TrustedKeys: serviceTrustedKeys()})
	if err == nil {
		t.Fatal("NewAdapterServerFromRoot() error = nil, want non-nil")
	}

	_, rpcErr := server.ListAdapters(context.Background(), &reinv1.ListAdaptersRequest{})
	if status.Code(rpcErr) != codes.FailedPrecondition {
		t.Fatalf("ListAdapters() status = %v, want %v", status.Code(rpcErr), codes.FailedPrecondition)
	}
}

func serviceAdapterIDs(adapters []*reinv1.Adapter) []string {
	ids := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		ids = append(ids, adapter.GetId())
	}
	return ids
}

func writeServiceFixture(t *testing.T, mismatch bool) string {
	t.Helper()

	root := t.TempDir()
	mustServiceMkdirAll(t, filepath.Join(root, ".claude-plugin"))

	writeServiceManifest(t, root, "messaging-local", map[string]any{
		"name":             "messaging-local",
		"version":          "0.6.0",
		"description":      "Messaging plugin",
		"category":         "messaging",
		"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
	})
	writeServiceManifest(t, root, "projection-local", map[string]any{
		"name":             "projection-local",
		"version":          "0.9.0",
		"description":      "Projection plugin",
		"category":         "projection",
		"daemonApiVersion": serviceDaemonVersion(mismatch),
		"tail":             true,
		"requires":         []string{"messaging.post"},
	})

	document := map[string]any{
		"name": "rein-service-fixture",
		"plugins": []any{
			map[string]any{
				"name":             "messaging-local",
				"source":           "./plugins/messaging-local",
				"version":          "0.6.0",
				"category":         "messaging",
				"daemonApiVersion": adapter.CurrentDaemonAPIVersion,
			},
			map[string]any{
				"name":             "projection-local",
				"source":           "./plugins/projection-local",
				"version":          "0.9.0",
				"category":         "projection",
				"daemonApiVersion": serviceDaemonVersion(mismatch),
				"tail":             true,
				"requires":         []string{"messaging.post"},
			},
		},
	}

	canonical, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	document["signature"] = map[string]any{
		"algorithm": "ed25519",
		"keyId":     "test-key",
		"value":     base64.StdEncoding.EncodeToString(ed25519.Sign(servicePrivateKey(), canonical)),
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

func writeServiceManifest(t *testing.T, root, name string, manifest map[string]any) {
	t.Helper()

	manifestDir := filepath.Join(root, "plugins", name, ".claude-plugin")
	mustServiceMkdirAll(t, manifestDir)

	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent(plugin.json) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), raw, 0o644); err != nil {
		t.Fatalf("WriteFile(plugin.json) error = %v", err)
	}
}

func mustServiceMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
}

func serviceDaemonVersion(mismatch bool) string {
	if mismatch {
		return "rein.v2"
	}
	return adapter.CurrentDaemonAPIVersion
}

func serviceTrustedKeys() map[string]ed25519.PublicKey {
	return map[string]ed25519.PublicKey{
		"test-key": servicePrivateKey().Public().(ed25519.PublicKey),
	}
}

func servicePrivateKey() ed25519.PrivateKey {
	seed := []byte("rein-rn11-marketplace-signing-se")
	return ed25519.NewKeyFromSeed(seed)
}
