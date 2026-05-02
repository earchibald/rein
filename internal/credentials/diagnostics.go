package credentials

import (
	"errors"
	"os/exec"
	"runtime"
)

type BuiltinReadiness struct {
	ExecutionScopeRequired bool                `json:"executionScopeRequired"`
	Providers              []ProviderReadiness `json:"providers"`
}

type ProviderReadiness struct {
	Scheme      string `json:"scheme"`
	Supported   bool   `json:"supported"`
	Available   bool   `json:"available"`
	Command     string `json:"command,omitempty"`
	CommandPath string `json:"commandPath,omitempty"`
	Error       string `json:"error,omitempty"`
}

func DiagnoseBuiltinReadiness() BuiltinReadiness {
	return diagnoseBuiltinReadiness(runtime.GOOS, exec.LookPath)
}

func diagnoseBuiltinReadiness(goos string, lookPath func(string) (string, error)) BuiltinReadiness {
	readiness := BuiltinReadiness{
		ExecutionScopeRequired: true,
		Providers: []ProviderReadiness{
			{Scheme: "env", Supported: true, Available: true},
			{Scheme: "file", Supported: true, Available: true},
		},
	}

	readiness.Providers = append(readiness.Providers, commandProviderReadiness(goos == "darwin", "keychain", "security", lookPath))
	readiness.Providers = append(readiness.Providers, commandProviderReadiness(goos == "linux", "libsecret", "secret-tool", lookPath))

	return readiness
}

func commandProviderReadiness(supported bool, scheme, command string, lookPath func(string) (string, error)) ProviderReadiness {
	readiness := ProviderReadiness{
		Scheme:    scheme,
		Supported: supported,
		Command:   command,
	}
	if !supported {
		return readiness
	}

	path, err := lookPath(command)
	if err == nil {
		readiness.Available = true
		readiness.CommandPath = path
		return readiness
	}

	if errors.Is(err, exec.ErrNotFound) {
		readiness.Error = err.Error()
		return readiness
	}

	readiness.Error = err.Error()
	return readiness
}
