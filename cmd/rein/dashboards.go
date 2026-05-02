package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/earchibald/rein/internal/dashboards"
)

func parseDashboardsApplyConfig(args []string, stderr io.Writer, lookupEnv func(string) (string, bool), getwd func() (string, error)) (dashboardsApplyConfig, error) {
	flagSet := flag.NewFlagSet("rein dashboards apply", flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	plugin := flagSet.String("plugin", dashboards.DefaultPluginName, "dashboards plugin name")
	signozURL := flagSet.String("signoz-url", firstEnv(lookupEnv, "SIGNOZ_BASE_URL", "SIGNOZ_URL"), "SigNoz base URL")
	signozAPIKey := flagSet.String("signoz-api-key", firstEnv(lookupEnv, "SIGNOZ_API_KEY"), "SigNoz API key")

	if err := flagSet.Parse(args); err != nil {
		return dashboardsApplyConfig{}, err
	}
	if flagSet.NArg() != 0 {
		return dashboardsApplyConfig{}, fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}

	workingDir, err := getwd()
	if err != nil {
		return dashboardsApplyConfig{}, fmt.Errorf("resolve working directory: %w", err)
	}
	rootPath, err := dashboards.FindRoot(workingDir)
	if err != nil {
		return dashboardsApplyConfig{}, err
	}

	config := dashboardsApplyConfig{
		Plugin:   strings.TrimSpace(*plugin),
		BaseURL:  strings.TrimSpace(*signozURL),
		APIKey:   strings.TrimSpace(*signozAPIKey),
		RootPath: rootPath,
	}
	if config.Plugin == "" {
		config.Plugin = dashboards.DefaultPluginName
	}
	if config.BaseURL == "" {
		return dashboardsApplyConfig{}, fmt.Errorf("SigNoz base URL is required")
	}
	if config.APIKey == "" {
		return dashboardsApplyConfig{}, fmt.Errorf("SigNoz API key is required")
	}
	return config, nil
}

func firstEnv(lookupEnv func(string) (string, bool), keys ...string) string {
	if lookupEnv == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := lookupEnv(key); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
