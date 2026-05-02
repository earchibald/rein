package credentials

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseReference(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("env://GITHUB_TOKEN")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	if reference.Scheme() != "env" {
		t.Fatalf("ParseReference() scheme = %q, want env", reference.Scheme())
	}

	if _, err := ParseReference("vault://team/app"); !errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("ParseReference() unsupported scheme error = %v, want %v", err, ErrUnsupportedScheme)
	}
	if _, err := ParseReference("not-a-uri"); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("ParseReference() invalid reference error = %v, want %v", err, ErrInvalidReference)
	}
}

func TestRegistryResolveRequiresExecutionScope(t *testing.T) {
	t.Parallel()

	registry := NewBuiltinRegistry(BuiltinOptions{})
	_, err := registry.Resolve(context.Background(), "env://TOKEN", ExecutionScope{})
	if !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("Resolve() scope error = %v, want %v", err, ErrInvalidScope)
	}
}

func TestRegistryResolveAllKeepsOpaqueReferencesSeparate(t *testing.T) {
	t.Parallel()

	references := map[string]string{
		"github_token": "env://RN15_GITHUB_TOKEN",
	}
	registry := NewBuiltinRegistry(BuiltinOptions{})

	resolved, err := registry.ResolveAll(context.Background(), references, ExecutionScope{
		ExecutionID: "exec-1",
		LookupEnv: func(name string) (string, bool) {
			if name != "RN15_GITHUB_TOKEN" {
				return "", false
			}
			return "super-secret-token", true
		},
	})
	if err != nil {
		t.Fatalf("ResolveAll() error = %v", err)
	}

	if references["github_token"] != "env://RN15_GITHUB_TOKEN" {
		t.Fatalf("ResolveAll() mutated reference map: %+v", references)
	}
	if resolved["github_token"] != "super-secret-token" {
		t.Fatalf("ResolveAll() resolved value = %q, want secret", resolved["github_token"])
	}
}

func TestEnvProviderRejectsMissingVariables(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("env://RN15_TOKEN")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}

	_, err = EnvProvider{}.Resolve(context.Background(), reference, ExecutionScope{
		ExecutionID: "exec-1",
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	if !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("Resolve() missing env error = %v, want %v", err, ErrCredentialNotFound)
	}
}

func TestFileProviderReadsSecretFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("file-secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	reference, err := ParseReference("file://" + path)
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}

	got, err := FileProvider{}.Resolve(context.Background(), reference, ExecutionScope{ExecutionID: "exec-1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "file-secret" {
		t.Fatalf("Resolve() = %q, want file-secret", got)
	}
}

func TestKeychainProviderUsesBackend(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("keychain://rein/github?keychain=login.keychain-db")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}

	backend := &fakeKeychainBackend{value: "kc-secret"}
	got, err := KeychainProvider{Backend: backend}.Resolve(context.Background(), reference, ExecutionScope{ExecutionID: "exec-1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "kc-secret" {
		t.Fatalf("Resolve() = %q, want kc-secret", got)
	}

	if backend.service != "rein" || backend.account != "github" || backend.keychain != "login.keychain-db" {
		t.Fatalf("backend locator = service=%q account=%q keychain=%q", backend.service, backend.account, backend.keychain)
	}
}

func TestLibsecretProviderUsesBackend(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("libsecret://rein/github?stage=prod")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}

	backend := &fakeLibsecretBackend{value: "ls-secret"}
	got, err := LibsecretProvider{Backend: backend}.Resolve(context.Background(), reference, ExecutionScope{ExecutionID: "exec-1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if got != "ls-secret" {
		t.Fatalf("Resolve() = %q, want ls-secret", got)
	}

	want := map[string]string{
		"service": "rein",
		"account": "github",
		"stage":   "prod",
	}
	if !reflect.DeepEqual(backend.attributes, want) {
		t.Fatalf("Lookup() attributes = %#v, want %#v", backend.attributes, want)
	}
}

func TestDefaultKeychainBackendBuildsSecurityCommand(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{output: []byte("kc-secret\n")}
	backend := NewDefaultKeychainBackend(runner, "darwin")

	got, err := backend.FindGenericPassword(context.Background(), "rein", "github", "/Users/test/login.keychain-db")
	if err != nil {
		t.Fatalf("FindGenericPassword() error = %v", err)
	}
	if got != "kc-secret" {
		t.Fatalf("FindGenericPassword() = %q, want kc-secret", got)
	}

	if runner.name != "security" {
		t.Fatalf("command name = %q, want security", runner.name)
	}
	wantArgs := []string{"find-generic-password", "-s", "rein", "-a", "github", "-w", "/Users/test/login.keychain-db"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestDefaultLibsecretBackendBuildsSecretToolCommand(t *testing.T) {
	t.Parallel()

	runner := &fakeCommandRunner{output: []byte("ls-secret\n")}
	backend := NewDefaultLibsecretBackend(runner, "linux")

	got, err := backend.Lookup(context.Background(), map[string]string{
		"service": "rein",
		"account": "github",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if got != "ls-secret" {
		t.Fatalf("Lookup() = %q, want ls-secret", got)
	}

	if runner.name != "secret-tool" {
		t.Fatalf("command name = %q, want secret-tool", runner.name)
	}
	wantArgs := []string{"lookup", "account", "github", "service", "rein"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("command args = %#v, want %#v", runner.args, wantArgs)
	}
}

func TestDefaultBackendsRejectUnsupportedPlatforms(t *testing.T) {
	t.Parallel()

	if _, err := NewDefaultKeychainBackend(&fakeCommandRunner{}, "linux").FindGenericPassword(context.Background(), "rein", "github", ""); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("FindGenericPassword() error = %v, want %v", err, ErrUnsupportedPlatform)
	}

	if _, err := NewDefaultLibsecretBackend(&fakeCommandRunner{}, "darwin").Lookup(context.Background(), map[string]string{"service": "rein"}); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("Lookup() error = %v, want %v", err, ErrUnsupportedPlatform)
	}
}

type fakeKeychainBackend struct {
	service  string
	account  string
	keychain string
	value    string
	err      error
}

func (b *fakeKeychainBackend) FindGenericPassword(_ context.Context, service, account, keychain string) (string, error) {
	b.service = service
	b.account = account
	b.keychain = keychain
	return b.value, b.err
}

type fakeLibsecretBackend struct {
	attributes map[string]string
	value      string
	err        error
}

func (b *fakeLibsecretBackend) Lookup(_ context.Context, attributes map[string]string) (string, error) {
	b.attributes = attributes
	return b.value, b.err
}

type fakeCommandRunner struct {
	name   string
	args   []string
	output []byte
	err    error
}

func (r *fakeCommandRunner) CombinedOutput(_ context.Context, name string, args ...string) ([]byte, error) {
	r.name = name
	r.args = append([]string(nil), args...)
	return r.output, r.err
}
