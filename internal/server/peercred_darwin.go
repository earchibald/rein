//go:build darwin

package server

import (
	"fmt"
	"log/slog"
	"net"

	"golang.org/x/sys/unix"
)

func wrapUnixPeerCredentialListener(listener net.Listener, logger *slog.Logger, expectedUID uint32) (net.Listener, error) {
	if expectedUID == 0 {
		currentUID, err := currentEffectiveUID()
		if err != nil {
			return nil, err
		}
		expectedUID = currentUID
	}

	return &peerCredentialListener{
		Listener:    listener,
		logger:      logger,
		expectedUID: expectedUID,
	}, nil
}

type peerCredentialListener struct {
	net.Listener
	logger      *slog.Logger
	expectedUID uint32
}

func (l *peerCredentialListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		unixConn, ok := conn.(*net.UnixConn)
		if !ok {
			_ = conn.Close()
			return nil, fmt.Errorf("peer credential authentication requires unix connections, got %T", conn)
		}

		credentials, err := readPeerCredentials(unixConn)
		if err != nil {
			l.logger.Warn("rejected unix peer connection", "error", err)
			_ = conn.Close()
			continue
		}
		if err := validatePeerCredentials(l.expectedUID, credentials); err != nil {
			l.logger.Warn(
				"rejected unix peer connection",
				"error", err,
				"peer_uid", credentials.UID,
				"expected_uid", l.expectedUID,
			)
			_ = conn.Close()
			continue
		}

		return conn, nil
	}
}

func readPeerCredentials(conn *net.UnixConn) (peerCredentials, error) {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return peerCredentials{}, fmt.Errorf("get unix syscall conn: %w", err)
	}

	var (
		uid        uint32
		gid        uint32
		controlErr error
	)

	if err := rawConn.Control(func(fd uintptr) {
		credentials, credErr := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if credErr != nil {
			controlErr = fmt.Errorf("%w: %v", ErrPeerCredentialUnavailable, credErr)
			return
		}
		uid = credentials.Uid
		if credentials.Ngroups > 0 {
			gid = credentials.Groups[0]
		}
	}); err != nil {
		return peerCredentials{}, fmt.Errorf("run unix syscall control: %w", err)
	}
	if controlErr != nil {
		return peerCredentials{}, fmt.Errorf("read LOCAL_PEERCRED: %w", controlErr)
	}

	return peerCredentials{
		UID: uid,
		GID: gid,
		PID: 0,
	}, nil
}
