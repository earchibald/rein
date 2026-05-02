package server

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

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

	harness := newBufconnHarness(t, Options{})

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), time.Second)
	defer rpcCancel()

	client := reinv1.NewProjectServiceClient(harness.conn)
	_, err := client.GetProject(rpcCtx, &reinv1.GetProjectRequest{Id: "project-1"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("GetProject() status = %v, want %v", status.Code(err), codes.Unimplemented)
	}
}

func TestRuntimeServeRejectsNilListener(t *testing.T) {
	t.Parallel()

	if err := New(Options{}).Serve(context.Background(), nil); err == nil {
		t.Fatal("Serve() error = nil, want non-nil")
	}
}
