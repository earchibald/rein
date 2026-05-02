package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"time"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/credentials"
	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

type doctorOutput struct {
	OK          bool                       `json:"ok"`
	GeneratedAt time.Time                  `json:"generatedAt"`
	Instance    doctorInstanceDiagnostic   `json:"instance"`
	Daemon      doctorDaemonDiagnostic     `json:"daemon"`
	Plugins     doctorPluginDiagnostic     `json:"plugins"`
	Credentials doctorCredentialDiagnostic `json:"credentials"`
	Storage     doctorStorageDiagnostic    `json:"storage"`
}

type doctorInstanceDiagnostic struct {
	Name         string                 `json:"name"`
	StateHome    string                 `json:"stateHome"`
	RootDir      string                 `json:"rootDir"`
	SocketPath   string                 `json:"socketPath"`
	DatabasePath string                 `json:"databasePath"`
	AutoStart    bool                   `json:"autoStart"`
	Layout       doctorLayoutDiagnostic `json:"layout"`
}

type doctorLayoutDiagnostic struct {
	Canonical      bool     `json:"canonical"`
	RootExists     bool     `json:"rootExists"`
	RootMode       string   `json:"rootMode,omitempty"`
	SocketExists   bool     `json:"socketExists"`
	SocketMode     string   `json:"socketMode,omitempty"`
	SocketIsSocket bool     `json:"socketIsSocket"`
	DatabaseExists bool     `json:"databaseExists"`
	DatabaseMode   string   `json:"databaseMode,omitempty"`
	Entries        []string `json:"entries,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type doctorDaemonDiagnostic struct {
	Network   string                 `json:"network"`
	Address   string                 `json:"address"`
	Reachable bool                   `json:"reachable"`
	RPC       doctorRPCDiagnostic    `json:"rpc"`
	Socket    doctorSocketDiagnostic `json:"socket"`
}

type doctorSocketDiagnostic struct {
	Path        string `json:"path,omitempty"`
	Exists      bool   `json:"exists"`
	IsSocket    bool   `json:"isSocket"`
	Mode        string `json:"mode,omitempty"`
	Connectable bool   `json:"connectable"`
	Error       string `json:"error,omitempty"`
}

type doctorRPCDiagnostic struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type doctorPluginDiagnostic struct {
	Root             string                      `json:"root"`
	DaemonAPIVersion string                      `json:"daemonApiVersion"`
	RegistryReady    bool                        `json:"registryReady"`
	AvailableCount   int                         `json:"availableCount"`
	ConfiguredCount  int                         `json:"configuredCount"`
	Error            string                      `json:"error,omitempty"`
	Marketplace      doctorMarketplaceDiagnostic `json:"marketplace"`
	Adapters         []doctorAdapterDiagnostic   `json:"adapters"`
}

type doctorMarketplaceDiagnostic struct {
	Path      string                    `json:"path"`
	Name      string                    `json:"name,omitempty"`
	Present   bool                      `json:"present"`
	Signature doctorSignatureDiagnostic `json:"signature"`
}

type doctorSignatureDiagnostic struct {
	Present   bool   `json:"present"`
	Verified  bool   `json:"verified"`
	Algorithm string `json:"algorithm,omitempty"`
	KeyID     string `json:"keyId,omitempty"`
	Error     string `json:"error,omitempty"`
}

type doctorAdapterDiagnostic struct {
	Name                 string         `json:"name"`
	Source               adapter.Source `json:"source"`
	LocalManifestPath    string         `json:"localManifestPath,omitempty"`
	LocalManifestFound   bool           `json:"localManifestFound"`
	Version              string         `json:"version,omitempty"`
	Category             string         `json:"category,omitempty"`
	DaemonAPIVersion     string         `json:"daemonApiVersion,omitempty"`
	CompatibleWithDaemon bool           `json:"compatibleWithDaemon"`
	Error                string         `json:"error,omitempty"`
}

type doctorCredentialDiagnostic struct {
	OK                     bool                            `json:"ok"`
	ExecutionScopeRequired bool                            `json:"executionScopeRequired"`
	Providers              []credentials.ProviderReadiness `json:"providers"`
}

type doctorStorageDiagnostic struct {
	OK         bool                       `json:"ok"`
	Database   doctorDatabaseDiagnostic   `json:"database"`
	Migrations sqlite.MigrationDiagnostic `json:"migrations"`
}

type doctorDatabaseDiagnostic struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Error  string `json:"error,omitempty"`
}

func (a *app) runDoctor(root rootConfig, args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		a.printDoctorHelp()
		return flag.ErrHelp
	}
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments: %v", args)
	}

	workingDir, err := a.getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	diagnostic := doctorOutput{
		GeneratedAt: time.Now().UTC(),
		Instance:    diagnoseInstance(root.instance),
		Daemon:      a.diagnoseDaemon(root.client, root.instance.SocketPath),
		Plugins:     diagnosePlugins(workingDir),
		Credentials: diagnoseCredentials(),
	}
	diagnostic.Storage = diagnoseStorage(root.instance.DatabasePath)
	diagnostic.OK = diagnostic.Instance.Layout.Canonical &&
		diagnostic.Instance.Layout.RootExists &&
		diagnostic.Daemon.RPC.OK &&
		diagnostic.Plugins.RegistryReady &&
		diagnostic.Storage.OK &&
		diagnostic.Credentials.OK

	return writeJSONObject(a.stdout, diagnostic)
}

func diagnoseInstance(layout instance.Layout) doctorInstanceDiagnostic {
	diagnostic := doctorInstanceDiagnostic{
		Name:         layout.Name,
		StateHome:    layout.StateHome,
		RootDir:      layout.RootDir,
		SocketPath:   layout.SocketPath,
		DatabasePath: layout.DatabasePath,
		AutoStart:    layout.AutoStartEnabled(),
	}

	layoutDiagnostic := doctorLayoutDiagnostic{Canonical: true}
	if err := layout.Validate(); err != nil {
		layoutDiagnostic.Canonical = false
		layoutDiagnostic.Error = err.Error()
	}

	if info, err := os.Stat(layout.RootDir); err == nil {
		layoutDiagnostic.RootExists = true
		layoutDiagnostic.RootMode = info.Mode().String()
		entries, readErr := os.ReadDir(layout.RootDir)
		if readErr != nil {
			layoutDiagnostic.Error = joinErrors(layoutDiagnostic.Error, readErr.Error())
		} else {
			layoutDiagnostic.Entries = make([]string, 0, len(entries))
			for _, entry := range entries {
				layoutDiagnostic.Entries = append(layoutDiagnostic.Entries, entry.Name())
			}
			sort.Strings(layoutDiagnostic.Entries)
		}
	} else if !os.IsNotExist(err) {
		layoutDiagnostic.Error = joinErrors(layoutDiagnostic.Error, err.Error())
	}

	if info, err := os.Lstat(layout.SocketPath); err == nil {
		layoutDiagnostic.SocketExists = true
		layoutDiagnostic.SocketMode = info.Mode().String()
		layoutDiagnostic.SocketIsSocket = info.Mode()&os.ModeSocket != 0
	} else if !os.IsNotExist(err) {
		layoutDiagnostic.Error = joinErrors(layoutDiagnostic.Error, err.Error())
	}

	if info, err := os.Stat(layout.DatabasePath); err == nil {
		layoutDiagnostic.DatabaseExists = true
		layoutDiagnostic.DatabaseMode = info.Mode().String()
	} else if !os.IsNotExist(err) {
		layoutDiagnostic.Error = joinErrors(layoutDiagnostic.Error, err.Error())
	}

	diagnostic.Layout = layoutDiagnostic
	return diagnostic
}

func (a *app) diagnoseDaemon(config clientConfig, socketPath string) doctorDaemonDiagnostic {
	diagnostic := doctorDaemonDiagnostic{
		Network: config.Network,
		Address: config.Address,
	}

	if config.Network == "unix" {
		diagnostic.Socket = diagnoseSocket(socketPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()

	conn, err := dialGRPC(ctx, config)
	if err != nil {
		diagnostic.RPC.Error = err.Error()
		return diagnostic
	}
	defer conn.Close()

	client := reinv1.NewProjectServiceClient(conn)
	if _, err := client.ListProjects(ctx, &reinv1.ListProjectsRequest{}); err != nil {
		diagnostic.RPC.Error = err.Error()
		return diagnostic
	}

	diagnostic.Reachable = true
	diagnostic.RPC.OK = true
	if config.Network == "unix" {
		diagnostic.Socket.Connectable = diagnostic.Socket.Exists && diagnostic.Socket.IsSocket
	}
	return diagnostic
}

func diagnoseSocket(path string) doctorSocketDiagnostic {
	diagnostic := doctorSocketDiagnostic{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		if !os.IsNotExist(err) {
			diagnostic.Error = err.Error()
		}
		return diagnostic
	}

	diagnostic.Exists = true
	diagnostic.Mode = info.Mode().String()
	diagnostic.IsSocket = info.Mode()&os.ModeSocket != 0
	if !diagnostic.IsSocket {
		diagnostic.Error = fmt.Sprintf("%q is not a unix socket", path)
		return diagnostic
	}

	conn, err := (&net.Dialer{Timeout: time.Second}).Dial("unix", path)
	if err != nil {
		diagnostic.Error = err.Error()
		return diagnostic
	}
	diagnostic.Connectable = true
	_ = conn.Close()
	return diagnostic
}

func diagnosePlugins(root string) doctorPluginDiagnostic {
	diagnostic := adapter.Diagnose(root, adapter.DiscoveryOptions{})

	adapters := make([]doctorAdapterDiagnostic, 0, len(diagnostic.Adapters))
	availableCount := 0
	for _, item := range diagnostic.Adapters {
		if item.Error == "" {
			availableCount++
		}
		adapters = append(adapters, doctorAdapterDiagnostic{
			Name:                 item.Name,
			Source:               item.Source,
			LocalManifestPath:    item.LocalManifestPath,
			LocalManifestFound:   item.LocalManifestFound,
			Version:              item.Version,
			Category:             item.Category,
			DaemonAPIVersion:     item.DaemonAPIVersion,
			CompatibleWithDaemon: item.CompatibleWithDaemon,
			Error:                item.Error,
		})
	}

	return doctorPluginDiagnostic{
		Root:             root,
		DaemonAPIVersion: adapter.CurrentDaemonAPIVersion,
		RegistryReady:    diagnostic.RegistryReady,
		AvailableCount:   availableCount,
		ConfiguredCount:  len(diagnostic.Adapters),
		Error:            diagnostic.Error,
		Marketplace: doctorMarketplaceDiagnostic{
			Path:    diagnostic.MarketplacePath,
			Name:    diagnostic.MarketplaceName,
			Present: diagnostic.MarketplaceFound,
			Signature: doctorSignatureDiagnostic{
				Present:   diagnostic.Signature.Present,
				Verified:  diagnostic.Signature.Verified,
				Algorithm: diagnostic.Signature.Algorithm,
				KeyID:     diagnostic.Signature.KeyID,
				Error:     diagnostic.Signature.Error,
			},
		},
		Adapters: adapters,
	}
}

func diagnoseCredentials() doctorCredentialDiagnostic {
	readiness := credentials.DiagnoseBuiltinReadiness()
	ok := true
	for _, provider := range readiness.Providers {
		if provider.Supported && !provider.Available {
			ok = false
		}
	}

	return doctorCredentialDiagnostic{
		OK:                     ok,
		ExecutionScopeRequired: readiness.ExecutionScopeRequired,
		Providers:              readiness.Providers,
	}
}

func diagnoseStorage(databasePath string) doctorStorageDiagnostic {
	migrations := sqlite.DiagnoseMigrations(context.Background(), sqlite.Config{Path: databasePath})
	database := doctorDatabaseDiagnostic{
		Path:   databasePath,
		Exists: migrations.Exists,
	}
	if migrations.InspectionError != "" {
		database.Error = migrations.InspectionError
	} else if migrations.BlockedReason != "" {
		database.Error = migrations.BlockedReason
	}

	return doctorStorageDiagnostic{
		OK:         migrations.Ready,
		Database:   database,
		Migrations: migrations,
	}
}

func writeJSONObject(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func joinErrors(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || current == next {
		return current
	}
	return current + "; " + next
}
