package credentials

import (
	"context"
	"fmt"
	"runtime"
	"sort"
)

type Provider interface {
	Scheme() string
	Resolve(context.Context, Reference, ExecutionScope) (string, error)
}

type Registry struct {
	providers map[string]Provider
}

type BuiltinOptions struct {
	KeychainBackend  KeychainBackend
	LibsecretBackend LibsecretBackend
}

func NewRegistry(providers ...Provider) (*Registry, error) {
	registry := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			continue
		}

		scheme := provider.Scheme()
		if _, exists := registry.providers[scheme]; exists {
			return nil, fmt.Errorf("credentials: duplicate provider for scheme %q", scheme)
		}
		registry.providers[scheme] = provider
	}
	return registry, nil
}

func NewBuiltinRegistry(options BuiltinOptions) *Registry {
	keychainBackend := options.KeychainBackend
	if keychainBackend == nil {
		keychainBackend = NewDefaultKeychainBackend(nil, runtime.GOOS)
	}

	libsecretBackend := options.LibsecretBackend
	if libsecretBackend == nil {
		libsecretBackend = NewDefaultLibsecretBackend(nil, runtime.GOOS)
	}

	registry, err := NewRegistry(
		EnvProvider{},
		FileProvider{},
		KeychainProvider{Backend: keychainBackend},
		LibsecretProvider{Backend: libsecretBackend},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *Registry) Resolve(ctx context.Context, rawReference string, scope ExecutionScope) (string, error) {
	reference, err := ParseReference(rawReference)
	if err != nil {
		return "", err
	}
	return r.ResolveReference(ctx, reference, scope)
}

func (r *Registry) ResolveReference(ctx context.Context, reference Reference, scope ExecutionScope) (string, error) {
	if err := scope.Validate(); err != nil {
		return "", err
	}

	provider, ok := r.providers[reference.Scheme()]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnsupportedScheme, reference.Scheme())
	}

	return provider.Resolve(ctx, reference, scope)
}

func (r *Registry) ResolveAll(ctx context.Context, references map[string]string, scope ExecutionScope) (map[string]string, error) {
	if len(references) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(references))
	for name := range references {
		names = append(names, name)
	}
	sort.Strings(names)

	resolved := make(map[string]string, len(references))
	for _, name := range names {
		value, err := r.Resolve(ctx, references[name], scope)
		if err != nil {
			return nil, fmt.Errorf("resolve credential %q: %w", name, err)
		}
		resolved[name] = value
	}

	return resolved, nil
}
