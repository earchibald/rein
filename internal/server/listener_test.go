package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListenUnixSocketCreates0600Socket(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	listener, err := Listen(ListenerConfig{
		Network:                "unix",
		Address:                socketPath,
		UnixSocketMode:         0o600,
		RequirePeerCredentials: false,
	})
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
	})

	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("os.Stat(%q) error = %v", socketPath, err)
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %#o, want %#o", got, 0o600)
	}
}

func TestListenUnixSocketRefusesToReplaceNonSocketPath(t *testing.T) {
	t.Parallel()

	socketPath := testSocketPath(t)
	t.Cleanup(func() {
		_ = os.Remove(socketPath)
	})
	if err := os.WriteFile(socketPath, []byte("not-a-socket"), 0o600); err != nil {
		t.Fatalf("os.WriteFile(%q) error = %v", socketPath, err)
	}

	_, err := Listen(ListenerConfig{
		Network:                "unix",
		Address:                socketPath,
		RequirePeerCredentials: false,
	})
	if err == nil {
		t.Fatal("Listen() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "refusing to replace non-socket") {
		t.Fatalf("Listen() error = %v, want non-socket refusal", err)
	}
}

func TestValidatePeerCredentialsRejectsDifferentUID(t *testing.T) {
	t.Parallel()

	err := validatePeerCredentials(42, peerCredentials{UID: 7})
	if err == nil {
		t.Fatal("validatePeerCredentials() error = nil, want non-nil")
	}
}

func testSocketPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(
		os.TempDir(),
		fmt.Sprintf("rein-%d-%d.sock", os.Getpid(), time.Now().UnixNano()),
	)
}
