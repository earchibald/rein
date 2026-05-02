//go:build linux || darwin

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

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

func TestListenUnixSocketWithPeerCredentialsServesCurrentUser(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	listener, err := Listen(ListenerConfig{
		Network:                "unix",
		Address:                socketPath,
		UnixSocketMode:         0o600,
		RequirePeerCredentials: true,
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	runtime := New(Options{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- runtime.Serve(ctx, listener)
	}()

	conn, err := grpc.NewClient(
		"passthrough:///"+socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		}),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	defer conn.Close()

	rpcCtx, rpcCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer rpcCancel()

	_, err = reinv1.NewWorkflowServiceClient(conn).ListWorkflows(rpcCtx, &reinv1.ListWorkflowsRequest{})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("ListWorkflows() status = %v, want %v", status.Code(err), codes.Unimplemented)
	}

	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
}
