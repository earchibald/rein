package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

func TestBackupHelperProcess(t *testing.T) {
	if os.Getenv("REIN_TEST_PID_HELPER") != "1" {
		t.Skip("helper process only")
	}

	pidPath := os.Getenv("REIN_TEST_PID_PATH")
	if pidPath == "" {
		t.Fatal("REIN_TEST_PID_PATH is required")
	}
	pidFile, err := instance.AcquirePIDFile(pidPath)
	if err != nil {
		t.Fatalf("AcquirePIDFile() error = %v", err)
	}
	defer pidFile.Close()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}

func TestBackupAndRestoreHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "backup",
			args: []string{"backup", "--help"},
			want: []string{"rein [global flags] backup [flags] <destination>", "Checkpoint SQLite WAL", "--stop bool"},
		},
		{
			name: "restore",
			args: []string{"restore", "--help"},
			want: []string{"rein [global flags] restore [flags] <source>", "Atomically replace the selected instance state directory", "--stop bool"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newTestApp(&stdout, &stderr, t.TempDir())
			err := app.run(tc.args)
			if err != flag.ErrHelp {
				t.Fatalf("run() error = %v, want %v", err, flag.ErrHelp)
			}
			for _, want := range tc.want {
				if !strings.Contains(stderr.String(), want) {
					t.Fatalf("help output missing %q\n%s", want, stderr.String())
				}
			}
		})
	}
}

func TestBackupCommandCheckpointsAndCopiesInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	stateHome := t.TempDir()
	layout := mustLayout(t, stateHome)
	store := openFileStore(t, ctx, layout.DatabasePath)
	if _, err := store.Create(ctx, sqlite.ProjectKind, "project-1", json.RawMessage(`{"name":"rein"}`)); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	backupPath := filepath.Join(t.TempDir(), "backup")
	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, envLookup(stateHome), nil, func() (string, error) {
		return t.TempDir(), nil
	})
	if err := app.run([]string{"backup", backupPath}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), backupPath) {
		t.Fatalf("stdout = %q, want backup path %q", stdout.String(), backupPath)
	}

	copiedStore := openFileStore(t, ctx, filepath.Join(backupPath, "rein.db"))
	if _, err := copiedStore.Get(ctx, sqlite.ProjectKind, "project-1"); err != nil {
		t.Fatalf("copied store Get() error = %v", err)
	}
	if info, err := os.Stat(filepath.Join(backupPath, "rein.db-wal")); err == nil && info.Size() != 0 {
		t.Fatalf("backup wal size = %d, want 0 after checkpoint", info.Size())
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup wal stat error = %v", err)
	}
}

func TestBackupStopStopsDaemonBeforeCopy(t *testing.T) {
	stateHome := t.TempDir()
	layout := mustLayout(t, stateHome)
	if err := os.WriteFile(filepath.Join(layout.RootDir, "state.txt"), []byte("ready"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestBackupHelperProcess")
	cmd.Env = append(os.Environ(),
		"REIN_TEST_PID_HELPER=1",
		"REIN_TEST_PID_PATH="+layout.PIDPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	helperExited := false
	defer func() {
		if helperExited {
			return
		}
		select {
		case <-done:
		default:
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	waitForPIDLock(t, layout.PIDPath)

	backupPath := filepath.Join(t.TempDir(), "backup")
	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, envLookup(stateHome), nil, func() (string, error) {
		return t.TempDir(), nil
	})
	if err := app.run([]string{"backup", "--stop", backupPath}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("helper exit error = %v", err)
	}
	helperExited = true
	if _, err := os.Stat(filepath.Join(backupPath, "state.txt")); err != nil {
		t.Fatalf("backup state file missing: %v", err)
	}
}

func TestRestoreCommandRejectsRunningDaemonWithoutStop(t *testing.T) {
	t.Parallel()

	stateHome := t.TempDir()
	layout := mustLayout(t, stateHome)
	pidFile, err := instance.AcquirePIDFile(layout.PIDPath)
	if err != nil {
		t.Fatalf("AcquirePIDFile() error = %v", err)
	}
	defer pidFile.Close()

	source := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, envLookup(stateHome), nil, func() (string, error) {
		return t.TempDir(), nil
	})
	err = app.run([]string{"restore", source})
	if err == nil || !strings.Contains(err.Error(), "daemon is running") {
		t.Fatalf("run() error = %v, want daemon running", err)
	}
}

func TestRestoreCommandReplacesInstanceState(t *testing.T) {
	t.Parallel()

	stateHome := t.TempDir()
	layout := mustLayout(t, stateHome)
	if err := os.WriteFile(filepath.Join(layout.RootDir, "old.txt"), []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(old) error = %v", err)
	}

	source := filepath.Join(t.TempDir(), "backup")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatalf("MkdirAll(source) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "rein.db"), []byte("db"), 0o600); err != nil {
		t.Fatalf("WriteFile(db) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "daemon.pid"), []byte("123\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(pid) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "grpc.sock"), []byte("socket"), 0o600); err != nil {
		t.Fatalf("WriteFile(socket) error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	app := newApp(&stdout, &stderr, envLookup(stateHome), nil, func() (string, error) {
		return t.TempDir(), nil
	})
	if err := app.run([]string{"restore", source}); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.RootDir, "rein.db")); err != nil {
		t.Fatalf("restored database missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.RootDir, "old.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old state stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(layout.PIDPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored pid stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(layout.SocketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restored socket stat error = %v, want not exist", err)
	}
}

func newTestApp(stdout, stderr *bytes.Buffer, stateHome string) *app {
	return newApp(stdout, stderr, envLookup(stateHome), nil, func() (string, error) {
		return tTempDir(stateHome), nil
	})
}

func envLookup(stateHome string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		if key == "XDG_STATE_HOME" {
			return stateHome, true
		}
		return "", false
	}
}

func mustLayout(t *testing.T, stateHome string) instance.Layout {
	t.Helper()
	layout, err := instance.NewLayout(instance.DefaultName, stateHome)
	if err != nil {
		t.Fatalf("NewLayout() error = %v", err)
	}
	if err := layout.EnsureRootDir(); err != nil {
		t.Fatalf("EnsureRootDir() error = %v", err)
	}
	return layout
}

func openFileStore(t *testing.T, ctx context.Context, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.OpenAndMigrate(ctx, sqlite.Config{Path: path})
	if err != nil {
		t.Fatalf("OpenAndMigrate(%q) error = %v", path, err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func waitForPIDLock(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pid, running, err := instance.ReadRunningPID(path)
		if err == nil && running && pid > 0 {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for running pid at %q", path)
}

func tTempDir(stateHome string) string {
	return filepath.Join(stateHome, "worktree")
}
