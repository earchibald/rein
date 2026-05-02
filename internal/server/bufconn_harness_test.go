package server

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const defaultBufconnListenerSize = 1024 * 1024

type bufconnHarness struct {
	conn     *grpc.ClientConn
	cancel   context.CancelFunc
	errCh    chan error
	runtime  *Runtime
	listener *bufconn.Listener
	tb       testing.TB
}

func newBufconnHarness(tb testing.TB, options Options, dialOptions ...grpc.DialOption) *bufconnHarness {
	tb.Helper()

	runtime := New(options)
	listener := bufconn.Listen(defaultBufconnListenerSize)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	go func() {
		errCh <- runtime.Serve(ctx, listener)
	}()

	optionsForDial := []grpc.DialOption{
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	optionsForDial = append(optionsForDial, dialOptions...)

	conn, err := grpc.NewClient("passthrough:///bufnet", optionsForDial...)
	if err != nil {
		cancel()
		runtime.Stop()
		_ = listener.Close()
		tb.Fatalf("grpc.NewClient() error = %v", err)
	}

	harness := &bufconnHarness{
		conn:     conn,
		cancel:   cancel,
		errCh:    errCh,
		runtime:  runtime,
		listener: listener,
		tb:       tb,
	}
	tb.Cleanup(harness.Close)

	return harness
}

func (h *bufconnHarness) Close() {
	if h.conn != nil {
		_ = h.conn.Close()
	}
	if h.cancel != nil {
		h.cancel()
	}
	if h.runtime != nil {
		h.runtime.Stop()
	}
	if h.errCh != nil {
		if err := <-h.errCh; err != nil && h.tb != nil {
			h.tb.Errorf("Runtime.Serve() error = %v", err)
		}
		h.errCh = nil
	}
	if h.listener != nil {
		_ = h.listener.Close()
	}
}
