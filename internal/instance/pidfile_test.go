//go:build darwin || linux

package instance

import (
	"path/filepath"
	"testing"
)

func TestPIDFileTracksRunningDaemon(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), pidFileName)
	pidFile, err := AcquirePIDFile(path)
	if err != nil {
		t.Fatalf("AcquirePIDFile() error = %v", err)
	}
	defer pidFile.Close()

	pid, running, err := ReadRunningPID(path)
	if err != nil {
		t.Fatalf("ReadRunningPID() error = %v", err)
	}
	if !running {
		t.Fatal("ReadRunningPID() running = false, want true")
	}
	if pid <= 0 {
		t.Fatalf("ReadRunningPID() pid = %d, want > 0", pid)
	}

	if err := pidFile.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	pid, running, err = ReadRunningPID(path)
	if err != nil {
		t.Fatalf("ReadRunningPID() after close error = %v", err)
	}
	if running || pid != 0 {
		t.Fatalf("ReadRunningPID() after close = (%d, %t), want (0, false)", pid, running)
	}
}
