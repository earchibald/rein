package main

import (
	"bytes"
	"flag"
	"io"
	"testing"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
)

func TestParseRuntimeConfigUsesDefaultLiveInstance(t *testing.T) {
	t.Parallel()

	config, err := parseRuntimeConfig(nil, io.Discard, nil, func() (string, error) {
		return "/Users/tester", nil
	})
	if err != nil {
		t.Fatalf("parseRuntimeConfig() error = %v", err)
	}

	if config.instance.Name != instance.DefaultName {
		t.Fatalf("instance name = %q, want %q", config.instance.Name, instance.DefaultName)
	}
	if config.listener.Network != server.DefaultListenerNetwork() {
		t.Fatalf("listener network = %q, want %q", config.listener.Network, server.DefaultListenerNetwork())
	}
	if config.listener.Network == "unix" && config.listener.Address != config.instance.SocketPath {
		t.Fatalf("listener address = %q, want %q", config.listener.Address, config.instance.SocketPath)
	}
}

func TestParseRuntimeConfigUsesEnvSelectedInstance(t *testing.T) {
	t.Parallel()

	config, err := parseRuntimeConfig(nil, io.Discard, func(key string) (string, bool) {
		switch key {
		case instance.EnvVar:
			return "staging", true
		case "XDG_STATE_HOME":
			return "/state", true
		default:
			return "", false
		}
	}, nil)
	if err != nil {
		t.Fatalf("parseRuntimeConfig() error = %v", err)
	}

	if config.instance.Name != "staging" {
		t.Fatalf("instance name = %q, want %q", config.instance.Name, "staging")
	}
	if config.listener.Address != config.instance.SocketPath {
		t.Fatalf("listener address = %q, want %q", config.listener.Address, config.instance.SocketPath)
	}
}

func TestParseRuntimeConfigFlagOverridesEnv(t *testing.T) {
	t.Parallel()

	config, err := parseRuntimeConfig([]string{"--instance", "dev"}, io.Discard, func(key string) (string, bool) {
		switch key {
		case instance.EnvVar:
			return "staging", true
		case "XDG_STATE_HOME":
			return "/state", true
		default:
			return "", false
		}
	}, nil)
	if err != nil {
		t.Fatalf("parseRuntimeConfig() error = %v", err)
	}

	if config.instance.Name != "dev" {
		t.Fatalf("instance name = %q, want %q", config.instance.Name, "dev")
	}
}

func TestParseRuntimeConfigUsesTCPDefaultsWhenRequested(t *testing.T) {
	t.Parallel()

	config, err := parseRuntimeConfig([]string{"--grpc-network", "tcp"}, io.Discard, func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return "/state", true
		}
		return "", false
	}, nil)
	if err != nil {
		t.Fatalf("parseRuntimeConfig() error = %v", err)
	}

	if config.listener.Network != "tcp" {
		t.Fatalf("listener network = %q, want %q", config.listener.Network, "tcp")
	}
	if config.listener.Address != server.DefaultListenerAddress("tcp") {
		t.Fatalf("listener address = %q, want %q", config.listener.Address, server.DefaultListenerAddress("tcp"))
	}
}

func TestParseRuntimeConfigHonorsExplicitAddress(t *testing.T) {
	t.Parallel()

	config, err := parseRuntimeConfig([]string{"--grpc-network", "unix", "--grpc-address", "/override.sock"}, io.Discard, func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return "/state", true
		}
		return "", false
	}, nil)
	if err != nil {
		t.Fatalf("parseRuntimeConfig() error = %v", err)
	}

	if config.listener.Address != "/override.sock" {
		t.Fatalf("listener address = %q, want %q", config.listener.Address, "/override.sock")
	}
}

func TestParseRuntimeConfigRejectsInvalidInstance(t *testing.T) {
	t.Parallel()

	_, err := parseRuntimeConfig(nil, io.Discard, func(key string) (string, bool) {
		switch key {
		case instance.EnvVar:
			return "../oops", true
		case "XDG_STATE_HOME":
			return "/state", true
		default:
			return "", false
		}
	}, nil)
	if err == nil {
		t.Fatal("parseRuntimeConfig() error = nil, want non-nil")
	}
}

func TestParseErrorExitCodeHandlesHelpAndDiagnostics(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	if got := parseErrorExitCode(flag.ErrHelp, &stderr); got != 0 {
		t.Fatalf("parseErrorExitCode(flag.ErrHelp) = %d, want 0", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty for help", stderr.String())
	}

	if got := parseErrorExitCode(instance.ErrInvalidName, &stderr); got != 2 {
		t.Fatalf("parseErrorExitCode(invalid) = %d, want 2", got)
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr = empty, want diagnostic output")
	}
}
