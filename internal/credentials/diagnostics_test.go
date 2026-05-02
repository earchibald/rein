package credentials

import (
	"os/exec"
	"testing"
)

func TestDiagnoseBuiltinReadinessMarksMissingCommandUnavailable(t *testing.T) {
	t.Parallel()

	readiness := diagnoseBuiltinReadiness("darwin", func(string) (string, error) {
		return "", exec.ErrNotFound
	})

	if len(readiness.Providers) != 4 {
		t.Fatalf("len(Providers) = %d, want 4", len(readiness.Providers))
	}
	if !readiness.ExecutionScopeRequired {
		t.Fatal("ExecutionScopeRequired = false, want true")
	}

	keychain := readiness.Providers[2]
	if keychain.Scheme != "keychain" || !keychain.Supported || keychain.Available || keychain.Error == "" {
		t.Fatalf("keychain readiness = %+v", keychain)
	}
}
