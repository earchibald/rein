// Package adaptertest provides reusable conformance checks for adapter plugins.
package adaptertest

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

// Spec describes a single adapter implementation to validate.
//
// Contract should be provided as either:
//   - a typed nil pointer to the required interface, e.g. (*MyContract)(nil), or
//   - a reflect.Type whose Kind is Interface.
type Spec struct {
	Category             reinv1.AdapterCategory
	Descriptor           *reinv1.Adapter
	Implementation       any
	Contract             any
	RequiredCapabilities []string
	Validate             func(testing.TB, *reinv1.Adapter, any)
}

func Run(t testing.TB, spec Spec) {
	t.Helper()

	if err := validate(spec); err != nil {
		t.Fatal(err)
	}

	if spec.Validate != nil {
		spec.Validate(t, spec.Descriptor, spec.Implementation)
	}
}

func RunCodingAgent(t testing.TB, spec Spec) {
	t.Helper()
	spec.Category = reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT
	Run(t, spec)
}

func RunReviewAgent(t testing.TB, spec Spec) {
	t.Helper()
	spec.Category = reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT
	Run(t, spec)
}

func RunTracker(t testing.TB, spec Spec) {
	t.Helper()
	spec.Category = reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER
	Run(t, spec)
}

func RunNotification(t testing.TB, spec Spec) {
	t.Helper()
	spec.Category = reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION
	Run(t, spec)
}

func RunMultiplexer(t testing.TB, spec Spec) {
	t.Helper()
	spec.Category = reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER
	Run(t, spec)
}

func validate(spec Spec) error {
	if spec.Category == reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED {
		return fmt.Errorf("category is required")
	}
	if spec.Descriptor == nil {
		return fmt.Errorf("descriptor is required")
	}
	if strings.TrimSpace(spec.Descriptor.GetId()) == "" {
		return fmt.Errorf("descriptor.id is required")
	}
	if strings.TrimSpace(spec.Descriptor.GetName()) == "" {
		return fmt.Errorf("descriptor.name is required")
	}
	if strings.TrimSpace(spec.Descriptor.GetVersion()) == "" {
		return fmt.Errorf("descriptor.version is required")
	}
	if spec.Descriptor.GetCategory() != spec.Category {
		return fmt.Errorf("descriptor category = %s, want %s", spec.Descriptor.GetCategory(), spec.Category)
	}
	if spec.Implementation == nil {
		return fmt.Errorf("implementation is required")
	}

	contractType, err := normalizeContractType(spec.Contract)
	if err != nil {
		return err
	}

	implementationType := reflect.TypeOf(spec.Implementation)
	if !implementationType.Implements(contractType) {
		if implementationType.Kind() != reflect.Pointer && reflect.PointerTo(implementationType).Implements(contractType) {
			return fmt.Errorf("implementation %T does not implement %s; pass a pointer value instead", spec.Implementation, contractType)
		}
		return fmt.Errorf("implementation %T does not implement %s", spec.Implementation, contractType)
	}

	if missing := missingCapabilities(spec.Descriptor.GetCapabilities(), spec.RequiredCapabilities); len(missing) > 0 {
		return fmt.Errorf("descriptor.capabilities missing %s", strings.Join(missing, ", "))
	}

	return nil
}

func normalizeContractType(contract any) (reflect.Type, error) {
	if contract == nil {
		return nil, fmt.Errorf("contract is required")
	}

	if contractType, ok := contract.(reflect.Type); ok {
		if contractType.Kind() != reflect.Interface {
			return nil, fmt.Errorf("contract type must be an interface, got %s", contractType)
		}
		return contractType, nil
	}

	contractType := reflect.TypeOf(contract)
	if contractType.Kind() == reflect.Pointer && contractType.Elem().Kind() == reflect.Interface {
		return contractType.Elem(), nil
	}

	return nil, fmt.Errorf("contract must be a typed nil pointer to an interface or a reflect.Type, got %T", contract)
}

func missingCapabilities(capabilities map[string]string, required []string) []string {
	missing := make([]string, 0, len(required))
	for _, capability := range required {
		if _, ok := capabilities[capability]; !ok {
			missing = append(missing, capability)
		}
	}

	slices.Sort(missing)
	return slices.Compact(missing)
}
