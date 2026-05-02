package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	config, err := parseRuntimeConfig(os.Args[1:], os.Stderr, os.LookupEnv, os.UserHomeDir)
	if err != nil {
		return parseErrorExitCode(err, os.Stderr)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	if err := config.instance.EnsureRootDir(); err != nil {
		logger.Error("failed to prepare instance state directory", "error", err)
		return 1
	}

	listenerConfig := config.listener
	listenerConfig.Logger = logger

	listener, err := server.Listen(listenerConfig)
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

	logger.Info(
		"rein gRPC server starting",
		"instance", config.instance.Name,
		"state_dir", config.instance.RootDir,
		"network", listenerConfig.Network,
		"address", listener.Addr().String(),
		"auto_start", config.instance.AutoStartEnabled(),
	)
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

func parseErrorExitCode(err error, stderr io.Writer) int {
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}

	_, _ = fmt.Fprintln(stderr, err)
	return 2
}
