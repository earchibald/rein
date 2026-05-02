package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiagnoseMigrationsReportsMissingDatabaseAsOutstanding(t *testing.T) {
	t.Parallel()

	diagnostic := DiagnoseMigrations(context.Background(), Config{Path: filepath.Join(t.TempDir(), "missing.db")})

	if diagnostic.Exists {
		t.Fatal("Exists = true, want false")
	}
	if diagnostic.Ready {
		t.Fatal("Ready = true, want false")
	}
	if got, want := diagnostic.Outstanding, []uint{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Outstanding = %v, want %v", got, want)
	}
}
