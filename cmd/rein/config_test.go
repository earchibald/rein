package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"path/filepath"
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

	config, err := parseDaemonServeConfig(root, []string{"--grpc-network", "unix", "--grpc-address", "/override.sock"}, io.Discard, nil)
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
	}, func() (string, error) {
		return "/repo", nil
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

func TestRootHelpListsStaticCommands(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	err := app.run([]string{"--help"})
	if err != flag.ErrHelp {
		t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
	}
	output := stderr.String()
	for _, want := range []string{
		"backup\tCheckpoint SQLite WAL",
		"rein dashboards apply [flags]",
		"dashboards apply\tApply reference OTLP dashboards to a SigNoz workspace.",
		"doctor\tEmit JSON diagnostics",
		"rein [global flags] describe-as=<format>",
		"describe-as=<format>\tEmit a stable machine-consumable surface description.",
		"restore\tAtomically replace the selected instance state from a backup copy.",
		"tui\tTerminal UI over the canonical gRPC surface.",
		"rein version [--json]",
		"version\tPrint the CLI version and embedded build provenance.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("help output missing %q\n%s", want, output)
		}
	}
}

func TestAppTUIHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	err := app.run([]string{"tui", "--help"})
	if err != flag.ErrHelp {
		t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
	}
	output := stderr.String()
	for _, want := range []string{
		"rein [global flags] tui",
		"Scroll the overview/drilldown pane when it overflows.",
		"Toggle compact vs expanded execution drilldown.",
		"Refresh daemon data.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("tui help missing %q\n%s", want, output)
		}
	}
}

func TestAppDashboardsHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	err := app.run([]string{"dashboards", "--help"})
	if err != flag.ErrHelp {
		t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
	}
	output := stderr.String()
	for _, want := range []string{
		"rein dashboards apply [flags]",
		"Apply the reference rein-dashboards plugin to a SigNoz API endpoint.",
		"--signoz-url string",
		"--signoz-api-key string",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dashboards help missing %q\n%s", want, output)
		}
	}
}

func TestParseDaemonServeConfigUsesTelemetryEnvAndFlagOverride(t *testing.T) {
	t.Parallel()

	root, _, err := parseRootConfig([]string{"daemon", "serve"}, io.Discard, func(key string) (string, bool) {
		switch key {
		case "XDG_STATE_HOME":
			return "/state", true
		case "OTEL_EXPORTER_OTLP_ENDPOINT":
			return "https://collector.example:4317", true
		case "OTEL_EXPORTER_OTLP_HEADERS":
			return "x-tenant=rein", true
		case "OTEL_EXPORTER_OTLP_INSECURE":
			return "false", true
		case "OTEL_SERVICE_NAME":
			return "rein-custom", true
		case "OTEL_RESOURCE_ATTRIBUTES":
			return "deployment.environment=dev", true
		default:
			return "", false
		}
	}, nil)
	if err != nil {
		t.Fatalf("parseRootConfig() error = %v", err)
	}

	config, err := parseDaemonServeConfig(root, []string{"--otlp-endpoint", "collector.internal:4317", "--otlp-insecure"}, io.Discard, func(key string) (string, bool) {
		switch key {
		case "OTEL_EXPORTER_OTLP_ENDPOINT":
			return "https://collector.example:4317", true
		case "OTEL_EXPORTER_OTLP_HEADERS":
			return "x-tenant=rein", true
		case "OTEL_EXPORTER_OTLP_INSECURE":
			return "false", true
		case "OTEL_SERVICE_NAME":
			return "rein-custom", true
		case "OTEL_RESOURCE_ATTRIBUTES":
			return "deployment.environment=dev", true
		default:
			return "", false
		}
	})
	if err != nil {
		t.Fatalf("parseDaemonServeConfig() error = %v", err)
	}

	if config.telemetry.Endpoint != "collector.internal:4317" {
		t.Fatalf("telemetry endpoint = %q, want collector.internal:4317", config.telemetry.Endpoint)
	}
	if !config.telemetry.Insecure {
		t.Fatal("telemetry insecure = false, want true")
	}
	if got := config.telemetry.Headers["x-tenant"]; got != "rein" {
		t.Fatalf("telemetry headers = %v, want x-tenant=rein", config.telemetry.Headers)
	}
	if config.telemetry.ServiceName != "rein-custom" {
		t.Fatalf("telemetry service name = %q, want rein-custom", config.telemetry.ServiceName)
	}
	if got := config.telemetry.ResourceAttributes["deployment.environment"]; got != "dev" {
		t.Fatalf("resource attributes = %v, want deployment.environment=dev", config.telemetry.ResourceAttributes)
	}
}

func TestParseDashboardsApplyConfigUsesEnv(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll(.claude-plugin) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "dashboards-marketplace.json"), []byte(`{"name":"rein-dashboards","plugins":[]}`), 0o644); err != nil {
		t.Fatalf("WriteFile(dashboards-marketplace.json) error = %v", err)
	}

	config, err := parseDashboardsApplyConfig(nil, io.Discard, func(key string) (string, bool) {
		switch key {
		case "SIGNOZ_BASE_URL":
			return "https://signoz.example", true
		case "SIGNOZ_API_KEY":
			return "secret", true
		default:
			return "", false
		}
	}, func() (string, error) {
		return filepath.Join(root, "subdir"), nil
	})
	if err != nil {
		t.Fatalf("parseDashboardsApplyConfig() error = %v", err)
	}
	if config.BaseURL != "https://signoz.example" {
		t.Fatalf("BaseURL = %q, want https://signoz.example", config.BaseURL)
	}
	if config.APIKey != "secret" {
		t.Fatalf("APIKey = %q, want secret", config.APIKey)
	}
	if config.RootPath != root {
		t.Fatalf("RootPath = %q, want %q", config.RootPath, root)
	}
}

func TestAppDescribeCLIOutput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	if err := app.run([]string{"describe-as=cli"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"REIN SURFACE v1",
		"COMMAND project list",
		"full_method: /rein.v1.ProjectService/ListProjects",
		"schema: rein.v1.PageRequest",
		"GATEWAY STUB",
		"MESSAGE rein.v1.ListProjectsRequest",
		"ENUM rein.v1.ProjectStatus",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("describe-as=cli output missing %q\n%s", want, output)
		}
	}
}

func TestAppDescribeMCPOutput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
		return "/Users/tester", nil
	}, func() (string, error) {
		return "/repo", nil
	})

	if err := app.run([]string{"describe-as=mcp"}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"surface: \"rein\"",
		"- name: \"project_list\"",
		"full_method: \"/rein.v1.ProjectService/ListProjects\"",
		"path: \"/v2/projects\"",
		"- name: \"rein.v1.ListProjectsRequest\"",
		"- name: \"rein.v1.ProjectStatus\"",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("describe-as=mcp output missing %q\n%s", want, output)
		}
	}
}

func TestAppDescribeHelpAndInvalidFormat(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
			return "/Users/tester", nil
		}, func() (string, error) {
			return "/repo", nil
		})

		err := app.run([]string{"describe-as", "--help"})
		if err != flag.ErrHelp {
			t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
		}
		output := stderr.String()
		for _, want := range []string{"Formats:", "cli", "mcp"} {
			if !strings.Contains(output, want) {
				t.Fatalf("describe help missing %q\n%s", want, output)
			}
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		app := newApp(&stdout, &stderr, func(string) (string, bool) { return "", false }, func() (string, error) {
			return "/Users/tester", nil
		}, func() (string, error) {
			return "/repo", nil
		})

		err := app.run([]string{"describe-as=bogus"})
		if err == nil {
			t.Fatal("run() error = nil, want non-nil")
		}
		if !strings.Contains(err.Error(), "supported: cli, mcp") {
			t.Fatalf("run() error = %v, want supported formats", err)
		}
	})
}
