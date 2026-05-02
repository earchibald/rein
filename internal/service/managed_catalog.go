package service

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/workflow"
)

func NewManagedCatalogFromRoot(root string, options adapter.DiscoveryOptions) (ManagedCatalog, error) {
	registry, err := adapter.Load(root, options)
	if err != nil {
		return nil, err
	}
	return NewManagedCatalog(registry), nil
}

func NewManagedCatalog(registry *adapter.Registry) ManagedCatalog {
	return &registryManagedCatalog{registry: registry}
}

type registryManagedCatalog struct {
	registry *adapter.Registry
}

func (c *registryManagedCatalog) List() []*reinv1.Adapter {
	if c == nil || c.registry == nil {
		return nil
	}
	return c.registry.List(reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED, false)
}

func (c *registryManagedCatalog) Lookup(id string) (ManagedAdapter, bool) {
	if c == nil || c.registry == nil {
		return nil, false
	}

	descriptor, ok := c.registry.Get(id)
	if !ok {
		return nil, false
	}
	return &registryManagedAdapter{descriptor: descriptor}, true
}

type registryManagedAdapter struct {
	descriptor *reinv1.Adapter
}

func (a *registryManagedAdapter) Descriptor() *reinv1.Adapter {
	if a == nil || a.descriptor == nil {
		return nil
	}
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *registryManagedAdapter) Run(context.Context, *workflow.RuntimeState, workflow.Phase, workflow.Direction, *workflow.SideEffect) error {
	if a == nil || a.descriptor == nil {
		return fmt.Errorf("adapter execution is not configured")
	}
	return fmt.Errorf("adapter %q does not support managed execution yet", a.descriptor.GetId())
}
