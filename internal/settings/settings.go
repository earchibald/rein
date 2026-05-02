package settings

import (
	"errors"
	"fmt"
	"sort"
)

type Layer string

const (
	LayerDaemonGlobal Layer = "daemon-global"
	LayerProject      Layer = "project"
	LayerWorkflow     Layer = "workflow"
	LayerExecution    Layer = "execution"

	DaemonGlobalScopeID = "daemon-global"
)

var (
	ErrDuplicateKey    = errors.New("settings: duplicate key")
	ErrDuplicateLayer  = errors.New("settings: duplicate layer")
	ErrEmptyLayers     = errors.New("settings: empty allowed layers")
	ErrInvalidKey      = errors.New("settings: invalid key")
	ErrInvalidLayer    = errors.New("settings: invalid layer")
	ErrInvalidScopeID  = errors.New("settings: invalid scope id")
	ErrLayerNotAllowed = errors.New("settings: layer not allowed")
	ErrUnknownKey      = errors.New("settings: unknown key")
	orderedLayers      = []Layer{LayerDaemonGlobal, LayerProject, LayerWorkflow, LayerExecution}
)

type KeySpec struct {
	Key           string
	AllowedLayers []Layer
}

type Registry struct {
	specs map[string]registryEntry
}

type registryEntry struct {
	allowed map[Layer]struct{}
}

type ScopedValues struct {
	Layer   Layer
	ScopeID string
	Values  map[string]string
}

type ResolvedValue struct {
	Value   string
	Layer   Layer
	ScopeID string
}

func NewRegistry(specs ...KeySpec) (Registry, error) {
	registry := Registry{
		specs: make(map[string]registryEntry, len(specs)),
	}

	for _, spec := range specs {
		if spec.Key == "" {
			return Registry{}, ErrInvalidKey
		}
		if _, exists := registry.specs[spec.Key]; exists {
			return Registry{}, fmt.Errorf("%w: %q", ErrDuplicateKey, spec.Key)
		}
		if len(spec.AllowedLayers) == 0 {
			return Registry{}, fmt.Errorf("%w: %q", ErrEmptyLayers, spec.Key)
		}

		allowed := make(map[Layer]struct{}, len(spec.AllowedLayers))
		for _, layer := range spec.AllowedLayers {
			if !isValidLayer(layer) {
				return Registry{}, fmt.Errorf("%w: %q", ErrInvalidLayer, layer)
			}
			allowed[layer] = struct{}{}
		}

		registry.specs[spec.Key] = registryEntry{allowed: allowed}
	}

	return registry, nil
}

func MustRegistry(specs ...KeySpec) Registry {
	registry, err := NewRegistry(specs...)
	if err != nil {
		panic(err)
	}

	return registry
}

func (r Registry) Validate(layer Layer, values map[string]string) error {
	if !isValidLayer(layer) {
		return fmt.Errorf("%w: %q", ErrInvalidLayer, layer)
	}

	for _, key := range sortedKeys(values) {
		entry, exists := r.specs[key]
		if !exists {
			return fmt.Errorf("%w: %q", ErrUnknownKey, key)
		}
		if _, allowed := entry.allowed[layer]; !allowed {
			return fmt.Errorf("%w: key %q at %q", ErrLayerNotAllowed, key, layer)
		}
	}

	return nil
}

func Resolve(registry Registry, scopes ...ScopedValues) (map[string]ResolvedValue, error) {
	byLayer := make(map[Layer]ScopedValues, len(scopes))
	for _, scope := range scopes {
		if !isValidLayer(scope.Layer) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidLayer, scope.Layer)
		}
		if _, exists := byLayer[scope.Layer]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateLayer, scope.Layer)
		}

		byLayer[scope.Layer] = ScopedValues{
			Layer:   scope.Layer,
			ScopeID: scope.ScopeID,
			Values:  cloneValues(scope.Values),
		}
	}

	resolved := make(map[string]ResolvedValue)
	for _, layer := range orderedLayers {
		scope, exists := byLayer[layer]
		if !exists {
			continue
		}
		if err := registry.Validate(layer, scope.Values); err != nil {
			return nil, err
		}

		for _, key := range sortedKeys(scope.Values) {
			resolved[key] = ResolvedValue{
				Value:   scope.Values[key],
				Layer:   layer,
				ScopeID: scope.ScopeID,
			}
		}
	}

	return resolved, nil
}

func NormalizeScopeID(layer Layer, scopeID string) (string, error) {
	if !isValidLayer(layer) {
		return "", fmt.Errorf("%w: %q", ErrInvalidLayer, layer)
	}

	switch layer {
	case LayerDaemonGlobal:
		if scopeID == "" || scopeID == DaemonGlobalScopeID {
			return DaemonGlobalScopeID, nil
		}
	case LayerProject, LayerWorkflow, LayerExecution:
		if scopeID != "" {
			return scopeID, nil
		}
	}

	return "", fmt.Errorf("%w: %q for %q", ErrInvalidScopeID, scopeID, layer)
}

func cloneValues(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}

	return cloned
}

func isValidLayer(layer Layer) bool {
	switch layer {
	case LayerDaemonGlobal, LayerProject, LayerWorkflow, LayerExecution:
		return true
	default:
		return false
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
