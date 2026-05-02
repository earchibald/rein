package adapter

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTrustedKeys(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	publicKey := privateKey().Public().(ed25519.PublicKey)
	document := `{
  "keys": [
    {
      "id": "fixture-key",
      "algorithm": "ed25519",
      "publicKey": "` + base64.StdEncoding.EncodeToString(publicKey) + `"
    }
  ]
}`
	if err := os.WriteFile(filepath.Join(root, trustedKeysPath), []byte(document), 0o644); err != nil {
		t.Fatalf("WriteFile(trusted-keys.json) error = %v", err)
	}

	keys, err := loadTrustedKeys(root)
	if err != nil {
		t.Fatalf("loadTrustedKeys() error = %v", err)
	}
	if got, want := len(keys), 1; got != want {
		t.Fatalf("len(loadTrustedKeys()) = %d, want %d", got, want)
	}
	if got := base64.StdEncoding.EncodeToString(keys["fixture-key"]); got != base64.StdEncoding.EncodeToString(publicKey) {
		t.Fatalf("fixture-key = %q, want %q", got, base64.StdEncoding.EncodeToString(publicKey))
	}
}

func TestLoadTrustedKeysRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, trustedKeysPath), []byte(`{"keys":[{"id":"broken","algorithm":"rsa","publicKey":"Zm9v"}]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(trusted-keys.json) error = %v", err)
	}

	_, err := loadTrustedKeys(root)
	if err == nil || err.Error() != `trusted key "broken" uses unsupported algorithm "rsa"` {
		t.Fatalf("loadTrustedKeys() error = %v, want unsupported algorithm error", err)
	}
}
