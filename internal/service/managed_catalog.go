package service

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
	muxadapter "github.com/earchibald/rein/internal/adapter/muxiterm"
	"github.com/earchibald/rein/internal/workflow"
)

func NewManagedCatalogFromRoot(root string, options adapter.DiscoveryOptions) (ManagedCatalog, error) {
	resolvedRoot, err := adapter.FindRoot(root)
	if err != nil {
		return nil, fmt.Errorf("discover adapter marketplace root: %w", err)
	}
	registry, err := adapter.Load(resolvedRoot, options)
	if err != nil {
		return nil, err
	}
	return newManagedCatalog(resolvedRoot, registry), nil
}

func NewManagedCatalog(registry *adapter.Registry) ManagedCatalog {
	return newManagedCatalog("", registry)
}

func newManagedCatalog(root string, registry *adapter.Registry) ManagedCatalog {
	return &registryManagedCatalog{
		root:     root,
		registry: registry,
		builtIns: map[string]func(*reinv1.Adapter) ManagedAdapter{
			muxadapter.AdapterID: func(descriptor *reinv1.Adapter) ManagedAdapter {
				managed := muxadapter.New()
				if descriptor != nil {
					return managed.WithDescriptor(descriptor)
				}
				return managed
			},
		},
	}
}

type registryManagedCatalog struct {
	root     string
	registry *adapter.Registry
	builtIns map[string]func(*reinv1.Adapter) ManagedAdapter
}

func (c *registryManagedCatalog) List() []*reinv1.Adapter {
	if c == nil {
		return nil
	}

	descriptors := map[string]*reinv1.Adapter{}
	if c.registry != nil {
		for _, descriptor := range c.registry.List(reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED, false) {
			descriptors[descriptor.GetId()] = descriptor
		}
	}
	for id, factory := range c.builtIns {
		descriptors[id] = factory(descriptors[id]).Descriptor()
	}

	ids := make([]string, 0, len(descriptors))
	for id := range descriptors {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	list := make([]*reinv1.Adapter, 0, len(ids))
	for _, id := range ids {
		list = append(list, descriptors[id])
	}
	return list
}

func (c *registryManagedCatalog) Lookup(id string) (ManagedAdapter, bool) {
	if c == nil {
		return nil, false
	}

	var entry adapter.Entry
	var ok bool
	if c.registry != nil {
		entry, ok = c.registry.Entry(id)
	}
	if factory, builtIn := c.builtIns[id]; builtIn {
		return factory(entry.Descriptor), true
	}
	if !ok {
		return nil, false
	}
	if managed, ok := builtinManagedAdapter(entry.Descriptor); ok {
		return managed, true
	}
	if factory, ok := managedAdapterFactories[id]; ok {
		return factory(c.root, entry.Descriptor), true
	}
	return &registryManagedAdapter{descriptor: entry.Descriptor, source: entry.Source}, true
}

func builtinManagedAdapter(descriptor *reinv1.Adapter) (ManagedAdapter, bool) {
	if descriptor == nil {
		return nil, false
	}

	switch descriptor.GetId() {
	case messagingNullAdapterID:
		return newManagedMessagingAdapter(descriptor, nil), true
	default:
		return nil, false
	}
}

type registryManagedAdapter struct {
	descriptor *reinv1.Adapter
	source     adapter.Source
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
	if repo := strings.TrimSpace(a.source.Repo); a.source.Kind == adapter.SourceGitHub && repo != "" {
		return fmt.Errorf("adapter %q is registered from GitHub repo %q, but remote managed execution is not wired into the daemon yet", a.descriptor.GetId(), repo)
	}
	if url := strings.TrimSpace(a.source.URL); (a.source.Kind == adapter.SourceURL || a.source.Kind == adapter.SourceGitSubdir) && url != "" {
		return fmt.Errorf("adapter %q is registered from remote source %q, but remote managed execution is not wired into the daemon yet", a.descriptor.GetId(), url)
	}
	return fmt.Errorf("adapter %q does not support managed execution yet", a.descriptor.GetId())
}
