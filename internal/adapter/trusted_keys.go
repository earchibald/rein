package adapter

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const trustedKeysPath = ".claude-plugin/trusted-keys.json"

type trustedKeysDocument struct {
	Keys []trustedKeyRecord `json:"keys"`
}

type trustedKeyRecord struct {
	ID        string `json:"id"`
	Algorithm string `json:"algorithm"`
	PublicKey string `json:"publicKey"`
}

func (o DiscoveryOptions) withRootDefaults(root string) (DiscoveryOptions, error) {
	options := o.withDefaults()

	trustedKeys, err := loadTrustedKeys(root)
	if err != nil {
		return DiscoveryOptions{}, err
	}
	if len(trustedKeys) == 0 {
		return options, nil
	}

	if len(options.TrustedKeys) == 0 {
		options.TrustedKeys = trustedKeys
		return options, nil
	}

	merged := make(map[string]ed25519.PublicKey, len(trustedKeys)+len(options.TrustedKeys))
	for keyID, publicKey := range trustedKeys {
		merged[keyID] = publicKey
	}
	for keyID, publicKey := range options.TrustedKeys {
		merged[keyID] = publicKey
	}
	options.TrustedKeys = merged
	return options, nil
}

func loadTrustedKeys(root string) (map[string]ed25519.PublicKey, error) {
	raw, err := os.ReadFile(filepath.Join(root, trustedKeysPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read trusted keys: %w", err)
	}

	var document trustedKeysDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode trusted keys: %w", err)
	}

	keys := make(map[string]ed25519.PublicKey, len(document.Keys))
	for i, record := range document.Keys {
		keyID := strings.TrimSpace(record.ID)
		if keyID == "" {
			return nil, fmt.Errorf("trusted keys[%d].id is required", i)
		}
		if record.Algorithm != "ed25519" {
			return nil, fmt.Errorf("trusted key %q uses unsupported algorithm %q", keyID, record.Algorithm)
		}
		if _, exists := keys[keyID]; exists {
			return nil, fmt.Errorf("trusted key %q is duplicated", keyID)
		}

		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(record.PublicKey))
		if err != nil {
			return nil, fmt.Errorf("decode trusted key %q: %w", keyID, err)
		}
		if len(decoded) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted key %q must decode to %d bytes", keyID, ed25519.PublicKeySize)
		}
		keys[keyID] = ed25519.PublicKey(decoded)
	}

	return keys, nil
}
