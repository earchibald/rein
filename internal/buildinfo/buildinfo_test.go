package buildinfo

import "testing"

func TestInfoFromSettingsPrefersExplicitStamping(t *testing.T) {
	t.Parallel()

	info := infoFromSettings(
		"2025.05.7",
		"abc123",
		"2025-05-02T03:04:05Z",
		"github-actions",
		map[string]string{
			"vcs.revision": "fallback",
			"vcs.time":     "2024-01-01T00:00:00Z",
			"vcs.modified": "true",
		},
	)

	if info.Version != "2025.05.7" {
		t.Fatalf("Version = %q, want 2025.05.7", info.Version)
	}
	if info.Commit != "abc123" {
		t.Fatalf("Commit = %q, want abc123", info.Commit)
	}
	if info.BuildTime != "2025-05-02T03:04:05Z" {
		t.Fatalf("BuildTime = %q, want stamped time", info.BuildTime)
	}
	if info.BuiltBy != "github-actions" {
		t.Fatalf("BuiltBy = %q, want github-actions", info.BuiltBy)
	}
	if !info.Modified {
		t.Fatal("Modified = false, want true")
	}
	if info.GoVersion == "" {
		t.Fatal("GoVersion = empty, want runtime version")
	}
	if info.Platform == "" {
		t.Fatal("Platform = empty, want runtime platform")
	}
}

func TestInfoFromSettingsFallsBackToVCSMetadata(t *testing.T) {
	t.Parallel()

	info := infoFromSettings("", "", "", "", map[string]string{
		"vcs.revision": "deadbeef",
		"vcs.time":     "2025-05-02T03:04:05Z",
	})

	if info.Version != "dev" {
		t.Fatalf("Version = %q, want dev", info.Version)
	}
	if info.Commit != "deadbeef" {
		t.Fatalf("Commit = %q, want deadbeef", info.Commit)
	}
	if info.BuildTime != "2025-05-02T03:04:05Z" {
		t.Fatalf("BuildTime = %q, want vcs time", info.BuildTime)
	}
	if info.BuiltBy != "local" {
		t.Fatalf("BuiltBy = %q, want local", info.BuiltBy)
	}
	if info.Modified {
		t.Fatal("Modified = true, want false")
	}
}
