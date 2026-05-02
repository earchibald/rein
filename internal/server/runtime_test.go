package server

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

func TestRuntimeRegistersAllServices(t *testing.T) {
	t.Parallel()

	runtime := New(Options{})

	got := runtime.GRPC().GetServiceInfo()
	want := []string{
		"rein.v1.AdapterService",
		"rein.v1.ExecutionService",
		"rein.v1.IssueService",
		"rein.v1.ProjectService",
		"rein.v1.WorkflowService",
	}

	for _, serviceName := range want {
		if _, ok := got[serviceName]; !ok {
			t.Fatalf("registered services missing %q", serviceName)
		}
	}

	if gateway := runtime.Gateway(); gateway == nil || len(gateway.Routes()) == 0 || len(gateway.Streams()) == 0 {
		t.Fatal("gateway stub was not initialized")
	}
}

func TestRuntimeServeBufconnReturnsUnimplemented(t *testing.T) {
	t.Parallel()

	runtime := New(Options{})
	listener := bufconn.Listen(1024 * 1024)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Serve(ctx, listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	defer conn.Close()

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()

	client := reinv1.NewProjectServiceClient(conn)
	_, err = client.GetProject(rpcCtx, &reinv1.GetProjectRequest{Id: "project-1"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("GetProject() status = %v, want %v", status.Code(err), codes.Unimplemented)
	}

	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}
