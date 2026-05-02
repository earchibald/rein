package service

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
)

type AdapterServer struct {
	reinv1.UnimplementedAdapterServiceServer
	registry *adapter.Registry
	loadErr  error
}

func NewAdapterServerFromRoot(root string, options adapter.DiscoveryOptions) (*AdapterServer, error) {
	registry, err := adapter.Load(root, options)
	server := &AdapterServer{
		registry: registry,
		loadErr:  err,
	}
	return server, err
}

func (s *AdapterServer) ListAdapters(_ context.Context, req *reinv1.ListAdaptersRequest) (*reinv1.ListAdaptersResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}

	return &reinv1.ListAdaptersResponse{
		Adapters: s.registry.List(req.GetCategory(), req.GetEnabledOnly()),
	}, nil
}

func (s *AdapterServer) GetAdapter(_ context.Context, req *reinv1.GetAdapterRequest) (*reinv1.GetAdapterResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	descriptor, ok := s.registry.Get(req.GetId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "adapter %q not found", req.GetId())
	}

	return &reinv1.GetAdapterResponse{Adapter: descriptor}, nil
}

func (s *AdapterServer) ValidateAdapter(_ context.Context, req *reinv1.ValidateAdapterRequest) (*reinv1.ValidateAdapterResponse, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if req.GetAdapter() == nil {
		return nil, status.Error(codes.InvalidArgument, "adapter is required")
	}

	messages := adapter.ValidateDescriptor(req.GetAdapter())
	return &reinv1.ValidateAdapterResponse{
		Valid:    len(messages) == 0,
		Messages: messages,
	}, nil
}

func (s *AdapterServer) ready() error {
	if s == nil {
		return status.Error(codes.FailedPrecondition, "adapter registry is not configured")
	}
	if s.loadErr != nil {
		return status.Errorf(codes.FailedPrecondition, "adapter registry unavailable: %v", s.loadErr)
	}
	return nil
}
