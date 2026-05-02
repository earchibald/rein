package main

import (
	"bytes"
	"strings"
	"testing"

	muxadapter "github.com/earchibald/rein/internal/adapter/muxiterm"
)

func TestLoadManagedCatalogWarnsWhenMarketplaceDiscoveryMissing(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	app := newApp(&bytes.Buffer{}, &stderr, func(string) (string, bool) { return "", false }, nil, nil)

	catalog, err := app.loadManagedCatalog()
	if err != nil {
		t.Fatalf("loadManagedCatalog() error = %v", err)
	}
	if catalog == nil {
		t.Fatal("loadManagedCatalog() catalog = nil, want built-in catalog")
	}

	managed, ok := catalog.Lookup(muxadapter.AdapterID)
	if !ok {
		t.Fatalf("Lookup(%q) = !ok", muxadapter.AdapterID)
	}
	if managed == nil {
		t.Fatalf("Lookup(%q) = nil, want built-in adapter", muxadapter.AdapterID)
	}
	if !strings.Contains(stderr.String(), `continuing with built-in adapters only`) {
		t.Fatalf("stderr = %q, want missing-marketplace warning", stderr.String())
	}
}
