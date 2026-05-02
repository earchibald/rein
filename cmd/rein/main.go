package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/server"
	"github.com/earchibald/rein/internal/service"
)

func main() {
	os.Exit(run())
}

func run() int {
	defaults := server.DefaultListenerConfig()

	network := flag.String("grpc-network", defaults.Network, "listener network: tcp or unix")
	address := flag.String("grpc-address", defaults.Address, "listener address or unix socket path")
	requirePeerCredentials := flag.Bool("grpc-require-peer-credentials", defaults.RequirePeerCredentials, "require SO_PEERCRED same-UID authentication for unix sockets")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	listener, err := server.Listen(server.ListenerConfig{
		Network:                *network,
		Address:                *address,
		UnixSocketMode:         0o600,
		RequirePeerCredentials: *requirePeerCredentials,
		Logger:                 logger,
	})
	if err != nil {
		logger.Error("failed to create listener", "error", err)
		return 1
	}
	defer func() {
		if err := listener.Close(); err != nil {
			logger.Error("failed to close listener", "error", err)
		}
	}()

	services, err := service.NewSetFromRoot(".", adapter.DiscoveryOptions{})
	if err != nil {
		logger.Error("failed to load adapter registry", "error", err)
		return 1
	}

	runtime := server.New(server.Options{Services: services})

	logger.Info("rein gRPC server starting", "network", *network, "address", listener.Addr().String())
	logger.Info(
		"rein HTTP/SSE gateway v2 stub ready",
		"routes", len(runtime.Gateway().Routes()),
		"streams", len(runtime.Gateway().Streams()),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := runtime.Serve(ctx, listener); err != nil {
		logger.Error("rein server exited with error", "error", err)
		return 1
	}

	return 0
}
