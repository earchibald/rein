package adapter

import (
	"path/filepath"
	"testing"
)

func TestDiagnoseReportsPluginCompatibilityFailures(t *testing.T) {
	t.Parallel()

	root := writeMarketplaceFixture(t, fixtureOptions{Signed: true, MismatchDaemonVersion: true})
	diagnostic := Diagnose(root, DiscoveryOptions{TrustedKeys: trustedKeys()})

	if diagnostic.RegistryReady {
		t.Fatal("RegistryReady = true, want false")
	}
	if len(diagnostic.Adapters) == 0 {
		t.Fatal("Adapters = empty, want entries")
	}

	var mismatch *AdapterDiagnostic
	for i := range diagnostic.Adapters {
		if diagnostic.Adapters[i].Name == "projection-local" {
			mismatch = &diagnostic.Adapters[i]
			break
		}
	}
	if mismatch == nil {
		t.Fatal("projection-local diagnostic missing")
	}
	if mismatch.CompatibleWithDaemon {
		t.Fatalf("CompatibleWithDaemon = true, want false: %+v", *mismatch)
	}
	if mismatch.Error == "" {
		t.Fatalf("Error = empty, want mismatch diagnostic: %+v", *mismatch)
	}
}

func TestDiagnoseFromWorkingDirReportsMissingMarketplace(t *testing.T) {
	t.Parallel()

	start := filepath.Join(t.TempDir(), "nested", "deeper")
	diagnostic := DiagnoseFromWorkingDir(start, DiscoveryOptions{AllowUnsignedIndex: true})

	if diagnostic.RootFound {
		t.Fatalf("RootFound = true, want false: %+v", diagnostic)
	}
	if diagnostic.RegistryReady {
		t.Fatalf("RegistryReady = true, want false: %+v", diagnostic)
	}
	want := `adapter marketplace ".claude-plugin/marketplace.json" was not found from "` + start + `"`
	if diagnostic.Error != want {
		t.Fatalf("Error = %q, want %q", diagnostic.Error, want)
	}
}
