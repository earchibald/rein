package instance

import "testing"

func TestResolveUsesCanonicalLayout(t *testing.T) {
	t.Parallel()

	layout, err := Resolve(ResolveOptions{
		Name: "import",
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
		UserHomeDir: func() (string, error) {
			return "/Users/tester", nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got, want := layout.RootDir, "/Users/tester/.local/state/rein/instances/import"; got != want {
		t.Fatalf("Resolve() root dir = %q, want %q", got, want)
	}
	if got, want := layout.DatabasePath, "/Users/tester/.local/state/rein/instances/import/rein.db"; got != want {
		t.Fatalf("Resolve() database path = %q, want %q", got, want)
	}
}

func TestNewLayoutRejectsRelativeStateHome(t *testing.T) {
	t.Parallel()

	if _, err := NewLayout("import", "relative"); err == nil {
		t.Fatal("NewLayout() error = nil, want error")
	}
}
