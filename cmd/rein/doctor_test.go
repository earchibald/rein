package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

func TestDoctorCommandEmitsStructuredJSON(t *testing.T) {
	t.Parallel()

	stateHome := t.TempDir()
	worktree := t.TempDir()

	layout, err := instance.NewLayout(instance.DefaultName, stateHome)
	if err != nil {
		t.Fatalf("NewLayout() error = %v", err)
	}
	if err := layout.EnsureRootDir(); err != nil {
		t.Fatalf("EnsureRootDir() error = %v", err)
	}
	if err := sqlite.MigrateUp(context.Background(), sqlite.Config{Path: layout.DatabasePath}); err != nil {
		t.Fatalf("MigrateUp() error = %v", err)
	}

	store, err := sqlite.OpenAndMigrate(context.Background(), sqlite.Config{Path: filepath.Join(t.TempDir(), "daemon.db")})
	if err != nil {
		t.Fatalf("OpenAndMigrate() daemon store error = %v", err)
	}
	defer store.Close()

	listener, err := server.Listen(server.ListenerConfig{Network: "tcp", Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	runtime := server.New(server.Options{Services: service.NewManagedSet(store, nil)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- runtime.Serve(ctx, listener)
	}()
	t.Cleanup(func() {
		cancel()
		runtime.Stop()
		select {
		case err := <-serveDone:
			if err != nil {
				t.Fatalf("Serve() error = %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out stopping runtime")
		}
	})

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return stateHome, true
		}
		return "", false
	}, nil, func() (string, error) {
		return worktree, nil
	})

	address := listener.Addr().String()
	if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
		address = tcpAddr.String()
	}
	err = app.run([]string{"--grpc-network", "tcp", "--grpc-address", address, "doctor"})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var payload struct {
		Instance struct {
			Name    string `json:"name"`
			PIDPath string `json:"pidPath"`
			Layout  struct {
				Canonical      bool `json:"canonical"`
				RootExists     bool `json:"rootExists"`
				DatabaseExists bool `json:"databaseExists"`
			} `json:"layout"`
		} `json:"instance"`
		Daemon struct {
			Reachable bool `json:"reachable"`
			RPC       struct {
				OK bool `json:"ok"`
			} `json:"rpc"`
		} `json:"daemon"`
		Plugins struct {
			Marketplace struct {
				Present bool `json:"present"`
			} `json:"marketplace"`
			ConfiguredCount int `json:"configuredCount"`
		} `json:"plugins"`
		Credentials struct {
			ExecutionScopeRequired bool `json:"executionScopeRequired"`
			Providers              []struct {
				Scheme string `json:"scheme"`
			} `json:"providers"`
		} `json:"credentials"`
		Storage struct {
			Migrations struct {
				Ready          bool `json:"ready"`
				CurrentVersion uint `json:"currentVersion"`
				LatestVersion  uint `json:"latestVersion"`
			} `json:"migrations"`
		} `json:"storage"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v\n%s", err, stdout.String())
	}

	if payload.Instance.Name != instance.DefaultName {
		t.Fatalf("instance name = %q, want %q", payload.Instance.Name, instance.DefaultName)
	}
	if payload.Instance.PIDPath != layout.PIDPath {
		t.Fatalf("instance pid path = %q, want %q", payload.Instance.PIDPath, layout.PIDPath)
	}
	if !payload.Instance.Layout.Canonical || !payload.Instance.Layout.RootExists || !payload.Instance.Layout.DatabaseExists {
		t.Fatalf("instance layout = %+v, want canonical existing layout", payload.Instance.Layout)
	}
	if !payload.Daemon.Reachable || !payload.Daemon.RPC.OK {
		t.Fatalf("daemon diagnostic = %+v, want reachable rpc ok", payload.Daemon)
	}
	if payload.Plugins.Marketplace.Present {
		t.Fatalf("marketplace.present = true, want false")
	}
	if payload.Plugins.ConfiguredCount != 0 {
		t.Fatalf("configuredCount = %d, want 0", payload.Plugins.ConfiguredCount)
	}
	if !payload.Credentials.ExecutionScopeRequired || len(payload.Credentials.Providers) < 2 {
		t.Fatalf("credentials diagnostic = %+v", payload.Credentials)
	}
	if !payload.Storage.Migrations.Ready || payload.Storage.Migrations.CurrentVersion == 0 || payload.Storage.Migrations.LatestVersion == 0 {
		t.Fatalf("migration diagnostic = %+v, want ready migrated database", payload.Storage.Migrations)
	}
}
