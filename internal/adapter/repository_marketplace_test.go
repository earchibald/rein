package adapter

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/earchibald/rein/adaptertest"
)

type repositoryCodingAgentContract interface {
	Run(context.Context, string) error
}

type repositoryCodingAgentStub struct{}

func (*repositoryCodingAgentStub) Run(context.Context, string) error {
	return nil
}

func TestRepositoryMarketplaceIncludesClaudeCodeBootstrap(t *testing.T) {
	t.Parallel()

	registry, err := Load(repositoryRoot(t), DiscoveryOptions{})
	if err != nil {
		t.Fatalf("Load(repositoryRoot) error = %v", err)
	}

	entry, ok := registry.Entry("coding-claude-code")
	if !ok {
		t.Fatal(`Entry("coding-claude-code") = !ok`)
	}
	if entry.Source.Kind != SourceGitHub {
		t.Fatalf("source kind = %q, want %q", entry.Source.Kind, SourceGitHub)
	}
	if got, want := entry.Source.Repo, "earchibald/rein-adapter-claude-code"; got != want {
		t.Fatalf("source repo = %q, want %q", got, want)
	}

	adaptertest.RunCodingAgent(t, adaptertest.Spec{
		Descriptor:           entry.Descriptor,
		Implementation:       &repositoryCodingAgentStub{},
		Contract:             (*repositoryCodingAgentContract)(nil),
		RequiredCapabilities: []string{"patch.apply", "pull_request", "shell.exec"},
	})
}

func repositoryRoot(t testing.TB) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() = !ok")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
