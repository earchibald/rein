package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
	"github.com/earchibald/rein/internal/telemetry"
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
	instance  instance.Layout
	listener  server.ListenerConfig
	telemetry telemetry.Config
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

func parseDaemonServeConfig(root rootConfig, args []string, stderr io.Writer, lookupEnv func(string) (string, bool)) (daemonServeConfig, error) {
	defaults := server.DefaultListenerConfig()
	telemetryConfig, rawHeaders, err := defaultTelemetryConfig(lookupEnv)
	if err != nil {
		return daemonServeConfig{}, err
	}
	flagSet := flag.NewFlagSet("rein daemon serve", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	network := flagSet.String("grpc-network", "", fmt.Sprintf("listener network: tcp or unix (default %q)", defaults.Network))
	address := flagSet.String("grpc-address", "", "listener address or unix socket path")
	requirePeerCredentials := flagSet.Bool("grpc-require-peer-credentials", defaults.RequirePeerCredentials, "require SO_PEERCRED same-UID authentication for unix sockets")
	otlpEndpoint := flagSet.String("otlp-endpoint", telemetryConfig.Endpoint, "optional OTLP/gRPC collector endpoint (host:port)")
	otlpHeaders := flagSet.String("otlp-headers", rawHeaders, "optional OTLP headers as comma-separated key=value pairs")
	otlpInsecure := flagSet.Bool("otlp-insecure", telemetryConfig.Insecure, "connect to the OTLP collector without TLS")

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
	if visited["otlp-endpoint"] {
		telemetryConfig.Endpoint = strings.TrimSpace(*otlpEndpoint)
	}
	if visited["otlp-insecure"] {
		telemetryConfig.Insecure = *otlpInsecure
	}
	if visited["otlp-headers"] {
		telemetryConfig.Headers, err = telemetry.ParseKeyValueList(*otlpHeaders)
		if err != nil {
			return daemonServeConfig{}, fmt.Errorf("parse --otlp-headers: %w", err)
		}
	}

	return daemonServeConfig{
		instance:  root.instance,
		listener:  listener,
		telemetry: telemetryConfig,
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

func defaultTelemetryConfig(lookupEnv func(string) (string, bool)) (telemetry.Config, string, error) {
	config := telemetry.Config{ServiceName: telemetry.DefaultServiceName}
	if lookupEnv == nil {
		return config, "", nil
	}

	if value, ok := lookupEnv("OTEL_EXPORTER_OTLP_ENDPOINT"); ok {
		config.Endpoint = strings.TrimSpace(value)
	}
	if value, ok := lookupEnv("OTEL_EXPORTER_OTLP_HEADERS"); ok {
		headers := strings.TrimSpace(value)
		parsed, err := telemetry.ParseKeyValueList(headers)
		if err != nil {
			return telemetry.Config{}, "", fmt.Errorf("parse OTEL_EXPORTER_OTLP_HEADERS: %w", err)
		}
		config.Headers = parsed
		return configureTelemetryAttributes(config, lookupEnv)
	}
	return configureTelemetryAttributes(config, lookupEnv)
}

func configureTelemetryAttributes(config telemetry.Config, lookupEnv func(string) (string, bool)) (telemetry.Config, string, error) {
	rawHeaders := ""
	if lookupEnv != nil {
		if value, ok := lookupEnv("OTEL_EXPORTER_OTLP_HEADERS"); ok {
			rawHeaders = strings.TrimSpace(value)
		}
		if value, ok := lookupEnv("OTEL_EXPORTER_OTLP_INSECURE"); ok && strings.TrimSpace(value) != "" {
			parsed, err := strconv.ParseBool(strings.TrimSpace(value))
			if err != nil {
				return telemetry.Config{}, "", fmt.Errorf("parse OTEL_EXPORTER_OTLP_INSECURE: %w", err)
			}
			config.Insecure = parsed
		}
		if value, ok := lookupEnv("OTEL_SERVICE_NAME"); ok && strings.TrimSpace(value) != "" {
			config.ServiceName = strings.TrimSpace(value)
		}
		if value, ok := lookupEnv("OTEL_RESOURCE_ATTRIBUTES"); ok && strings.TrimSpace(value) != "" {
			attributes, err := telemetry.ParseKeyValueList(value)
			if err != nil {
				return telemetry.Config{}, "", fmt.Errorf("parse OTEL_RESOURCE_ATTRIBUTES: %w", err)
			}
			config.ResourceAttributes = attributes
		}
	}
	return config, rawHeaders, nil
}
