package credentials

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
)

var ErrUnsupportedPlatform = errors.New("credentials: unsupported platform")

type EnvProvider struct{}

func (EnvProvider) Scheme() string {
	return "env"
}

func (EnvProvider) Resolve(_ context.Context, reference Reference, scope ExecutionScope) (string, error) {
	name, err := envVariableName(reference)
	if err != nil {
		return "", err
	}

	value, ok := scope.lookupEnv(name)
	if !ok {
		return "", fmt.Errorf("%w: env %q", ErrCredentialNotFound, name)
	}

	return value, nil
}

type FileProvider struct{}

func (FileProvider) Scheme() string {
	return "file"
}

func (FileProvider) Resolve(_ context.Context, reference Reference, _ ExecutionScope) (string, error) {
	path, err := filePath(reference)
	if err != nil {
		return "", err
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("credentials: read file %q: %w", path, err)
	}

	return string(content), nil
}

type KeychainBackend interface {
	FindGenericPassword(ctx context.Context, service, account, keychain string) (string, error)
}

type KeychainProvider struct {
	Backend KeychainBackend
}

func (KeychainProvider) Scheme() string {
	return "keychain"
}

func (p KeychainProvider) Resolve(ctx context.Context, reference Reference, _ ExecutionScope) (string, error) {
	service, account, keychain, err := keychainLocator(reference)
	if err != nil {
		return "", err
	}

	backend := p.Backend
	if backend == nil {
		backend = NewDefaultKeychainBackend(nil, runtime.GOOS)
	}

	value, err := backend.FindGenericPassword(ctx, service, account, keychain)
	if err != nil {
		return "", fmt.Errorf("credentials: resolve %s: %w", reference.String(), err)
	}

	return value, nil
}

type LibsecretBackend interface {
	Lookup(ctx context.Context, attributes map[string]string) (string, error)
}

type LibsecretProvider struct {
	Backend LibsecretBackend
}

func (LibsecretProvider) Scheme() string {
	return "libsecret"
}

func (p LibsecretProvider) Resolve(ctx context.Context, reference Reference, _ ExecutionScope) (string, error) {
	attributes, err := libsecretAttributes(reference)
	if err != nil {
		return "", err
	}

	backend := p.Backend
	if backend == nil {
		backend = NewDefaultLibsecretBackend(nil, runtime.GOOS)
	}

	value, err := backend.Lookup(ctx, attributes)
	if err != nil {
		return "", fmt.Errorf("credentials: resolve %s: %w", reference.String(), err)
	}

	return value, nil
}

type CommandRunner interface {
	CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error)
}

func NewDefaultKeychainBackend(runner CommandRunner, goos string) KeychainBackend {
	if runner == nil {
		runner = execCommandRunner{}
	}
	return commandKeychainBackend{
		runner: runner,
		goos:   goos,
	}
}

func NewDefaultLibsecretBackend(runner CommandRunner, goos string) LibsecretBackend {
	if runner == nil {
		runner = execCommandRunner{}
	}
	return commandLibsecretBackend{
		runner: runner,
		goos:   goos,
	}
}

type execCommandRunner struct{}

func (execCommandRunner) CombinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type commandKeychainBackend struct {
	runner CommandRunner
	goos   string
}

func (b commandKeychainBackend) FindGenericPassword(ctx context.Context, service, account, keychain string) (string, error) {
	if b.goos != "darwin" {
		return "", ErrUnsupportedPlatform
	}

	args := []string{"find-generic-password", "-s", service, "-a", account, "-w"}
	if keychain != "" {
		args = append(args, keychain)
	}

	output, err := runCommand(ctx, b.runner, "security", args...)
	if err != nil {
		return "", err
	}

	return trimSingleTrailingNewline(string(output)), nil
}

type commandLibsecretBackend struct {
	runner CommandRunner
	goos   string
}

func (b commandLibsecretBackend) Lookup(ctx context.Context, attributes map[string]string) (string, error) {
	if b.goos != "linux" {
		return "", ErrUnsupportedPlatform
	}

	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	slices.Sort(keys)

	args := []string{"lookup"}
	for _, key := range keys {
		args = append(args, key, attributes[key])
	}

	output, err := runCommand(ctx, b.runner, "secret-tool", args...)
	if err != nil {
		return "", err
	}

	return trimSingleTrailingNewline(string(output)), nil
}

func envVariableName(reference Reference) (string, error) {
	if reference.Host() != "" && strings.TrimPrefix(reference.Path(), "/") != "" {
		return "", fmt.Errorf("%w: env references must use either host or path", ErrInvalidReference)
	}

	name := strings.TrimSpace(reference.Host())
	if name == "" {
		name = strings.TrimPrefix(strings.TrimSpace(reference.Path()), "/")
	}
	if !isEnvironmentVariableName(name) {
		return "", fmt.Errorf("%w: invalid env variable name %q", ErrInvalidReference, name)
	}

	return name, nil
}

func filePath(reference Reference) (string, error) {
	host := reference.Host()
	if host != "" && host != "localhost" {
		return "", fmt.Errorf("%w: file references must use an empty host or localhost", ErrInvalidReference)
	}

	path := reference.Path()
	if path == "" {
		return "", fmt.Errorf("%w: file path is required", ErrInvalidReference)
	}

	return path, nil
}

func keychainLocator(reference Reference) (service, account, keychain string, err error) {
	service = strings.TrimSpace(reference.Host())
	account = strings.TrimPrefix(strings.TrimSpace(reference.Path()), "/")
	if service == "" || account == "" {
		return "", "", "", fmt.Errorf("%w: keychain references must use keychain://service/account", ErrInvalidReference)
	}

	query := reference.Query()
	for key, values := range query {
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return "", "", "", fmt.Errorf("%w: query %q must have exactly one non-empty value", ErrInvalidReference, key)
		}
		if key != "keychain" {
			return "", "", "", fmt.Errorf("%w: unsupported keychain query %q", ErrInvalidReference, key)
		}
		keychain = values[0]
	}

	return service, account, keychain, nil
}

func libsecretAttributes(reference Reference) (map[string]string, error) {
	attributes := map[string]string{}

	if service := strings.TrimSpace(reference.Host()); service != "" {
		attributes["service"] = service
	}
	if account := strings.TrimPrefix(strings.TrimSpace(reference.Path()), "/"); account != "" {
		attributes["account"] = account
	}

	for key, values := range reference.Query() {
		if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
			return nil, fmt.Errorf("%w: query %q must have exactly one non-empty value", ErrInvalidReference, key)
		}
		attributes[key] = values[0]
	}

	if len(attributes) == 0 {
		return nil, fmt.Errorf("%w: libsecret references require at least one lookup attribute", ErrInvalidReference)
	}

	return attributes, nil
}

func isEnvironmentVariableName(name string) bool {
	if name == "" {
		return false
	}

	for index, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case index > 0 && r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}

	return true
}

func runCommand(ctx context.Context, runner CommandRunner, name string, args ...string) ([]byte, error) {
	output, err := runner.CombinedOutput(ctx, name, args...)
	if err == nil {
		return output, nil
	}

	if trimmed := strings.TrimSpace(string(output)); trimmed != "" {
		return nil, fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), trimmed, err)
	}

	return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

func trimSingleTrailingNewline(value string) string {
	if strings.HasSuffix(value, "\r\n") {
		return strings.TrimSuffix(value, "\r\n")
	}
	return strings.TrimSuffix(value, "\n")
}
