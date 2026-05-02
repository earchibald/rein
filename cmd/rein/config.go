package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
)

type rootConfig struct {
	instance instance.Layout
	client   clientConfig
}

type clientConfig struct {
	Network string
	Address string
}

type daemonServeConfig struct {
	instance instance.Layout
	listener server.ListenerConfig
}

func parseRootConfig(args []string, stderr io.Writer, lookupEnv func(string) (string, bool), userHomeDir func() (string, error)) (rootConfig, []string, error) {
	flagSet := flag.NewFlagSet("rein", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	instanceName := flagSet.String("instance", "", fmt.Sprintf("instance name (default %q or %s)", instance.DefaultName, instance.EnvVar))
	network := flagSet.String("grpc-network", "", "daemon network override: tcp or unix")
	address := flagSet.String("grpc-address", "", "daemon address override")

	if err := flagSet.Parse(args); err != nil {
		return rootConfig{}, nil, err
	}

	layout, err := instance.Resolve(instance.ResolveOptions{
		Name:        *instanceName,
		LookupEnv:   lookupEnv,
		UserHomeDir: userHomeDir,
	})
	if err != nil {
		return rootConfig{}, nil, err
	}

	client := defaultClientConfig(layout)
	visited := visitedFlags(flagSet)
	if visited["grpc-network"] {
		client.Network = *network
		client.Address = defaultConnectionAddress(client.Network, layout)
	}
	if visited["grpc-address"] {
		client.Address = *address
	}

	return rootConfig{
		instance: layout,
		client:   client,
	}, flagSet.Args(), nil
}

func parseDaemonServeConfig(root rootConfig, args []string, stderr io.Writer) (daemonServeConfig, error) {
	defaults := server.DefaultListenerConfig()
	flagSet := flag.NewFlagSet("rein daemon serve", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	network := flagSet.String("grpc-network", "", fmt.Sprintf("listener network: tcp or unix (default %q)", defaults.Network))
	address := flagSet.String("grpc-address", "", "listener address or unix socket path")
	requirePeerCredentials := flagSet.Bool("grpc-require-peer-credentials", defaults.RequirePeerCredentials, "require SO_PEERCRED same-UID authentication for unix sockets")

	if err := flagSet.Parse(args); err != nil {
		return daemonServeConfig{}, err
	}
	if flagSet.NArg() != 0 {
		return daemonServeConfig{}, fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}

	listener := defaults
	listener.Address = defaultConnectionAddress(listener.Network, root.instance)
	visited := visitedFlags(flagSet)
	if visited["grpc-network"] {
		listener.Network = *network
		listener.Address = defaultConnectionAddress(listener.Network, root.instance)
	}
	if visited["grpc-address"] {
		listener.Address = *address
	}
	listener.RequirePeerCredentials = *requirePeerCredentials
	listener.UnixSocketMode = 0o600

	return daemonServeConfig{
		instance: root.instance,
		listener: listener,
	}, nil
}

func defaultClientConfig(layout instance.Layout) clientConfig {
	network := server.DefaultListenerNetwork()
	return clientConfig{
		Network: network,
		Address: defaultConnectionAddress(network, layout),
	}
}

func defaultConnectionAddress(network string, layout instance.Layout) string {
	if network == "unix" {
		return layout.SocketPath
	}
	return server.DefaultListenerAddress(network)
}

func visitedFlags(flagSet *flag.FlagSet) map[string]bool {
	visited := map[string]bool{}
	flagSet.Visit(func(f *flag.Flag) {
		visited[f.Name] = true
	})
	return visited
}
