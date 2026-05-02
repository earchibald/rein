//go:build darwin || linux

package instance

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrDaemonAlreadyRunning = errors.New("instance: daemon already running")

// PIDFile holds the advisory lock that marks an instance daemon as running.
type PIDFile struct {
	path string
	file *os.File
}

func AcquirePIDFile(path string) (*PIDFile, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("instance: open pid file %q: %w", path, err)
	}

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if isLockHeld(err) {
			return nil, fmt.Errorf("%w: %s", ErrDaemonAlreadyRunning, path)
		}
		return nil, fmt.Errorf("instance: lock pid file %q: %w", path, err)
	}

	if err := file.Truncate(0); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("instance: truncate pid file %q: %w", path, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("instance: seek pid file %q: %w", path, err)
	}
	if _, err := fmt.Fprintf(file, "%d\n", os.Getpid()); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("instance: write pid file %q: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, fmt.Errorf("instance: sync pid file %q: %w", path, err)
	}

	return &PIDFile{path: path, file: file}, nil
}

func ReadRunningPID(path string) (pid int, running bool, err error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("instance: open pid file %q: %w", path, err)
	}
	defer file.Close()

	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		return 0, false, nil
	} else if !isLockHeld(err) {
		return 0, false, fmt.Errorf("instance: inspect pid file lock %q: %w", path, err)
	}

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, false, fmt.Errorf("instance: seek pid file %q: %w", path, err)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return 0, false, fmt.Errorf("instance: read pid file %q: %w", path, err)
	}

	pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false, fmt.Errorf("instance: parse pid file %q: %w", path, err)
	}

	return pid, true, nil
}

func (p *PIDFile) Close() error {
	if p == nil || p.file == nil {
		return nil
	}

	lockErr := unix.Flock(int(p.file.Fd()), unix.LOCK_UN)
	closeErr := p.file.Close()
	removeErr := os.Remove(p.path)
	if errors.Is(removeErr, os.ErrNotExist) {
		removeErr = nil
	}
	p.file = nil

	if lockErr != nil || closeErr != nil || removeErr != nil {
		return errors.Join(lockErr, closeErr, removeErr)
	}
	return nil
}

func isLockHeld(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
