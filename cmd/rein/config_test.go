package main

import (
	"bytes"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
)

func TestParseRootConfigUsesDefaultLiveInstance(t *testing.T) {
	t.Parallel()

	config, args, err := parseRootConfig([]string{"project", "list"}, io.Discard, nil, func() (string, error) {
		return "/Users/tester", nil
	})
	if err != nil {
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	if len(args) != 2 || args[0] != "project" || args[1] != "list" {
		t.Fatalf("remaining args = %v, want project list", args)
	}
	if config.instance.Name != instance.DefaultName {
		t.Fatalf("instance name = %q, want %q", config.instance.Name, instance.DefaultName)
	}
	if config.client.Network != server.DefaultListenerNetwork() {
		t.Fatalf("client network = %q, want %q", config.client.Network, server.DefaultListenerNetwork())
	}
	if config.client.Network == "unix" && config.client.Address != config.instance.SocketPath {
		t.Fatalf("client address = %q, want %q", config.client.Address, config.instance.SocketPath)
	}
}

func TestParseRootConfigUsesEnvSelectedInstance(t *testing.T) {
	t.Parallel()

	config, _, err := parseRootConfig([]string{"project", "list"}, io.Discard, func(key string) (string, bool) {
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
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	if config.instance.Name != "staging" {
		t.Fatalf("instance name = %q, want staging", config.instance.Name)
	}
	if config.client.Address != config.instance.SocketPath {
		t.Fatalf("client address = %q, want %q", config.client.Address, config.instance.SocketPath)
	}
}

func TestParseRootConfigFlagOverridesEnv(t *testing.T) {
	t.Parallel()

	config, _, err := parseRootConfig([]string{"--instance", "dev", "project", "list"}, io.Discard, func(key string) (string, bool) {
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
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	if config.instance.Name != "dev" {
		t.Fatalf("instance name = %q, want dev", config.instance.Name)
	}
}

func TestParseRootConfigUsesTCPDefaultsWhenRequested(t *testing.T) {
	t.Parallel()

	config, _, err := parseRootConfig([]string{"--grpc-network", "tcp", "project", "list"}, io.Discard, func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return "/state", true
		}
		return "", false
	}, nil)
	if err != nil {
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	if config.client.Network != "tcp" {
		t.Fatalf("client network = %q, want tcp", config.client.Network)
	}
	if config.client.Address != server.DefaultListenerAddress("tcp") {
		t.Fatalf("client address = %q, want %q", config.client.Address, server.DefaultListenerAddress("tcp"))
	}
}

func TestParseDaemonServeConfigHonorsExplicitAddress(t *testing.T) {
	t.Parallel()

	root, _, err := parseRootConfig([]string{"daemon", "serve"}, io.Discard, func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return "/state", true
		}
		return "", false
	}, nil)
	if err != nil {
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	config, err := parseDaemonServeConfig(root, []string{"--grpc-network", "unix", "--grpc-address", "/override.sock"}, io.Discard)
	if err != nil {
		t.Fatalf("parseDaemonServeConfig() error = %v", err)
	}

	if config.listener.Address != "/override.sock" {
		t.Fatalf("listener address = %q, want /override.sock", config.listener.Address)
	}
}

func TestParseRootConfigRejectsInvalidInstance(t *testing.T) {
	t.Parallel()

	_, _, err := parseRootConfig([]string{"project", "list"}, io.Discard, func(key string) (string, bool) {
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
		t.Fatal("parseRootConfig() error = nil, want non-nil")
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

func TestAppCommandHelpUsesProtoComments(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	})

	err := app.run([]string{"project", "list", "--help"})
	if err != flag.ErrHelp {
		t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
	}

	output := stderr.String()
	for _, want := range []string{
		"List projects stored in the daemon.",
		"--status enum",
		"Filter projects by status.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output missing %q\n%s", want, output)
		}
	}
}
