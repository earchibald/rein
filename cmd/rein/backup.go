package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

const stateCommandTimeout = 30 * time.Second

type stateTransferConfig struct {
	stop bool
	path string
}

func (a *app) runBackup(root rootConfig, args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		a.printBackupHelp()
		return flag.ErrHelp
	}
	config, err := parseStateTransferConfig("rein backup", args, a.stderr)
	if err != nil {
		return err
	}
	if config.stop {
		if err := stopInstanceDaemon(root.instance, stateCommandTimeout); err != nil {
			return fmt.Errorf("stop selected daemon: %w", err)
		}
	}
	if err := checkpointInstanceDatabase(root.instance, stateCommandTimeout); err != nil {
		return fmt.Errorf("checkpoint selected instance: %w", err)
	}

	destination, err := a.resolveCLIPath(config.path)
	if err != nil {
		return err
	}
	if err := instance.AtomicCopyDir(root.instance.RootDir, destination, instance.CopyDirOptions{Filter: instance.SkipRuntimeArtifacts}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.stdout, "backup created at %s\n", destination)
	return err
}

func (a *app) runRestore(root rootConfig, args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		a.printRestoreHelp()
		return flag.ErrHelp
	}
	config, err := parseStateTransferConfig("rein restore", args, a.stderr)
	if err != nil {
		return err
	}

	running, err := daemonRunning(root.instance)
	if err != nil {
		return err
	}
	if running && !config.stop {
		return fmt.Errorf("selected instance daemon is running; stop it first or pass --stop")
	}
	if config.stop {
		if err := stopInstanceDaemon(root.instance, stateCommandTimeout); err != nil {
			return fmt.Errorf("stop selected daemon: %w", err)
		}
	}

	source, err := a.resolveCLIPath(config.path)
	if err != nil {
		return err
	}
	if err := instance.AtomicReplaceDir(source, root.instance.RootDir, instance.CopyDirOptions{Filter: instance.SkipRuntimeArtifacts}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.stdout, "restored instance state from %s\n", source)
	return err
}

func parseStateTransferConfig(name string, args []string, stderr io.Writer) (stateTransferConfig, error) {
	flagSet := flag.NewFlagSet(name, flag.ContinueOnError)
	flagSet.SetOutput(stderr)

	stop := flagSet.Bool("stop", false, "stop the selected daemon first")
	if err := flagSet.Parse(args); err != nil {
		return stateTransferConfig{}, err
	}
	if flagSet.NArg() != 1 {
		return stateTransferConfig{}, fmt.Errorf("exactly one path is required")
	}
	return stateTransferConfig{stop: *stop, path: flagSet.Arg(0)}, nil
}

func checkpointInstanceDatabase(layout instance.Layout, timeout time.Duration) error {
	if _, err := os.Stat(layout.DatabasePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect database %q: %w", layout.DatabasePath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if _, err := sqlite.CheckpointWAL(ctx, sqlite.Config{Path: layout.DatabasePath}); err != nil {
		return err
	}
	return nil
}

func daemonRunning(layout instance.Layout) (bool, error) {
	_, running, err := instance.ReadRunningPID(layout.PIDPath)
	if err != nil {
		return false, err
	}
	if running {
		return true, nil
	}
	return socketReachable(layout.SocketPath), nil
}

func stopInstanceDaemon(layout instance.Layout, timeout time.Duration) error {
	pid, running, err := instance.ReadRunningPID(layout.PIDPath)
	if err != nil {
		return err
	}
	if !running {
		if socketReachable(layout.SocketPath) {
			return fmt.Errorf("daemon reachable at %q but pid file is unavailable; stop it manually", layout.SocketPath)
		}
		return nil
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process %d: %w", pid, err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && !isProcessGone(err) {
		return fmt.Errorf("signal daemon process %d: %w", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		_, running, err := instance.ReadRunningPID(layout.PIDPath)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for daemon process %d to stop", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func socketReachable(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	dialer := net.Dialer{Timeout: 250 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func isProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}

func (a *app) resolveCLIPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	workingDir, err := a.getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	return filepath.Clean(filepath.Join(workingDir, path)), nil
}
