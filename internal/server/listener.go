package server

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
)

const (
	defaultUnixSocketMode fs.FileMode = 0o600
	defaultTCPAddress                 = "127.0.0.1:7777"
)

type ListenerConfig struct {
	Network                string
	Address                string
	UnixSocketMode         fs.FileMode
	RequirePeerCredentials bool
	Logger                 *slog.Logger
}

func DefaultListenerConfig() ListenerConfig {
	network := DefaultListenerNetwork()

	return ListenerConfig{
		Network:                network,
		Address:                DefaultListenerAddress(network),
		UnixSocketMode:         defaultUnixSocketMode,
		RequirePeerCredentials: network == "unix",
	}
}

func DefaultListenerNetwork() string {
	if runtime.GOOS == "linux" || runtime.GOOS == "darwin" {
		return "unix"
	}

	return "tcp"
}

func DefaultListenerAddress(network string) string {
	switch network {
	case "unix":
		return filepath.Join(os.TempDir(), "rein.sock")
	case "tcp":
		return defaultTCPAddress
	default:
		return ""
	}
}

func Listen(config ListenerConfig) (net.Listener, error) {
	config = config.withDefaults()

	switch config.Network {
	case "tcp":
		var listenConfig net.ListenConfig
		return listenConfig.Listen(context.Background(), "tcp", config.Address)
	case "unix":
		return listenUnix(config)
	default:
		return nil, fmt.Errorf("unsupported listener network %q", config.Network)
	}
}

func (c ListenerConfig) withDefaults() ListenerConfig {
	defaults := DefaultListenerConfig()

	if c.Network == "" {
		c.Network = defaults.Network
	}
	if c.Address == "" {
		c.Address = DefaultListenerAddress(c.Network)
		if c.Address == "" {
			c.Address = defaults.Address
		}
	}
	if c.UnixSocketMode == 0 {
		c.UnixSocketMode = defaultUnixSocketMode
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}

	return c
}

func listenUnix(config ListenerConfig) (net.Listener, error) {
	if config.Address == "" {
		return nil, errors.New("unix socket path is required")
	}

	if err := os.MkdirAll(filepath.Dir(config.Address), 0o755); err != nil {
		return nil, fmt.Errorf("create unix socket directory: %w", err)
	}
	if err := removeStaleUnixSocket(config.Address); err != nil {
		return nil, err
	}

	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(context.Background(), "unix", config.Address)
	if err != nil {
		return nil, fmt.Errorf("listen on unix socket %q: %w", config.Address, err)
	}

	cleanupListener := &unixSocketListener{
		Listener: listener,
		path:     config.Address,
	}

	if err := os.Chmod(config.Address, config.UnixSocketMode); err != nil {
		_ = cleanupListener.Close()
		return nil, fmt.Errorf("chmod unix socket %q: %w", config.Address, err)
	}

	if !config.RequirePeerCredentials {
		return cleanupListener, nil
	}

	authenticatedListener, err := wrapUnixPeerCredentialListener(cleanupListener, config.Logger, 0)
	if err != nil {
		_ = cleanupListener.Close()
		return nil, err
	}

	return authenticatedListener, nil
}

func removeStaleUnixSocket(path string) error {
	info, err := os.Lstat(path)
	if err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("refusing to replace non-socket path %q", path)
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove stale unix socket %q: %w", path, err)
		}
		return nil
	}
	if os.IsNotExist(err) {
		return nil
	}

	return fmt.Errorf("inspect unix socket path %q: %w", path, err)
}

type unixSocketListener struct {
	net.Listener
	path string
}

func (l *unixSocketListener) Close() error {
	closeErr := l.Listener.Close()
	removeErr := os.Remove(l.path)
	if os.IsNotExist(removeErr) {
		removeErr = nil
	}

	return errors.Join(closeErr, removeErr)
}
