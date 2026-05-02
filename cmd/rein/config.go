package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
)

type runtimeConfig struct {
	instance instance.Layout
	listener server.ListenerConfig
}

func parseRuntimeConfig(args []string, stderr io.Writer, lookupEnv func(string) (string, bool), userHomeDir func() (string, error)) (runtimeConfig, error) {
	defaults := server.DefaultListenerConfig()
	flagSet := flag.NewFlagSet("rein", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	instanceName := flagSet.String("instance", "", fmt.Sprintf("instance name (default %q or %s)", instance.DefaultName, instance.EnvVar))
	network := flagSet.String("grpc-network", "", fmt.Sprintf("listener network: tcp or unix (default %q)", defaults.Network))
	address := flagSet.String("grpc-address", "", "listener address or unix socket path")
	requirePeerCredentials := flagSet.Bool("grpc-require-peer-credentials", defaults.RequirePeerCredentials, "require SO_PEERCRED same-UID authentication for unix sockets")

	if err := flagSet.Parse(args); err != nil {
		return runtimeConfig{}, err
	}

	layout, err := instance.Resolve(instance.ResolveOptions{
		Name:        *instanceName,
		LookupEnv:   lookupEnv,
		UserHomeDir: userHomeDir,
	})
	if err != nil {
		return runtimeConfig{}, err
	}

	visited := map[string]bool{}
	flagSet.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})

	listener := defaults
	if visited["grpc-network"] {
		listener.Network = *network
	}
	listener.Address = defaultListenerAddress(listener.Network, layout)
	if visited["grpc-address"] {
		listener.Address = *address
	}
	listener.RequirePeerCredentials = *requirePeerCredentials
	listener.UnixSocketMode = 0o600

	return runtimeConfig{
		instance: layout,
		listener: listener,
	}, nil
}

func defaultListenerAddress(network string, layout instance.Layout) string {
	if network == "unix" {
		return layout.SocketPath
	}

	return server.DefaultListenerAddress(network)
}
