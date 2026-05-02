package server

import (
	"context"
	"errors"
	"net"

	"google.golang.org/grpc"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/gateway"
	"github.com/earchibald/rein/internal/service"
)

type Options struct {
	Services    service.Set
	Gateway     *gateway.V2Stub
	GRPCOptions []grpc.ServerOption
}

type Runtime struct {
	grpcServer *grpc.Server
	gateway    *gateway.V2Stub
}

func New(options Options) *Runtime {
	services := options.Services.WithDefaults()
	grpcOptions := append([]grpc.ServerOption(nil), options.GRPCOptions...)

	grpcServer := grpc.NewServer(grpcOptions...)
	RegisterServices(grpcServer, services)

	gatewayStub := options.Gateway
	if gatewayStub == nil {
		gatewayStub = gateway.NewV2Stub()
	}

	return &Runtime{
		grpcServer: grpcServer,
		gateway:    gatewayStub,
	}
}

func RegisterServices(registrar grpc.ServiceRegistrar, services service.Set) {
	services = services.WithDefaults()

	reinv1.RegisterAdapterServiceServer(registrar, services.Adapter)
	reinv1.RegisterExecutionServiceServer(registrar, services.Execution)
	reinv1.RegisterIssueServiceServer(registrar, services.Issue)
	reinv1.RegisterProjectServiceServer(registrar, services.Project)
	reinv1.RegisterWorkflowServiceServer(registrar, services.Workflow)
}

func (r *Runtime) GRPC() *grpc.Server {
	return r.grpcServer
}

func (r *Runtime) Gateway() *gateway.V2Stub {
	return r.gateway
}

func (r *Runtime) Serve(ctx context.Context, listener net.Listener) error {
	if listener == nil {
		return errors.New("listener is required")
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			r.Stop()
		case <-done:
		}
	}()

	err := r.grpcServer.Serve(listener)
	if err == nil || errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, net.ErrClosed) {
		return nil
	}

	return err
}

func (r *Runtime) Stop() {
	r.grpcServer.GracefulStop()
}
