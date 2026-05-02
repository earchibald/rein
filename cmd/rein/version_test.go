package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/earchibald/rein/internal/buildinfo"
)

func TestAppVersionCommandEmitsText(t *testing.T) {
	restore := setBuildStampForTest(t, "2025.05.7", "abc123", "2025-05-02T03:04:05Z", "github-actions")
	defer restore()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	if err := app.run([]string{"version"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"rein 2025.05.7",
		"commit: abc123",
		"build_time: 2025-05-02T03:04:05Z",
		"built_by: github-actions",
		"go_version:",
		"platform:",
		"modified: false",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("version output missing %q\n%s", want, output)
		}
	}
}

func TestAppVersionCommandEmitsJSON(t *testing.T) {
	restore := setBuildStampForTest(t, "2025.05.7", "abc123", "2025-05-02T03:04:05Z", "github-actions")
	defer restore()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	if err := app.run([]string{"version", "--json"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var payload buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.String())
	}
	if payload.Version != "2025.05.7" {
		t.Fatalf("Version = %q, want 2025.05.7", payload.Version)
	}
	if payload.Commit != "abc123" {
		t.Fatalf("Commit = %q, want abc123", payload.Commit)
	}
	if payload.BuildTime != "2025-05-02T03:04:05Z" {
		t.Fatalf("BuildTime = %q, want stamped time", payload.BuildTime)
	}
	if payload.BuiltBy != "github-actions" {
		t.Fatalf("BuiltBy = %q, want github-actions", payload.BuiltBy)
	}
}

func setBuildStampForTest(t *testing.T, version, commit, buildTime, builtBy string) func() {
	t.Helper()

	previousVersion := buildinfo.Version
	previousCommit := buildinfo.Commit
	previousBuildTime := buildinfo.BuildTime
	previousBuiltBy := buildinfo.BuiltBy

	buildinfo.Version = version
	buildinfo.Commit = commit
	buildinfo.BuildTime = buildTime
	buildinfo.BuiltBy = builtBy

	return func() {
		buildinfo.Version = previousVersion
		buildinfo.Commit = previousCommit
		buildinfo.BuildTime = previousBuildTime
		buildinfo.BuiltBy = previousBuiltBy
	}
}
