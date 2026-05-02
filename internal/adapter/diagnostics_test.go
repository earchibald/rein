package adapter

import "testing"

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
