//go:build !linux && !darwin

package server

import (
	"errors"
	"log/slog"
	"net"
)

func wrapUnixPeerCredentialListener(_ net.Listener, _ *slog.Logger, _ uint32) (net.Listener, error) {
	return nil, errors.New("peer credential authentication is only supported on linux and darwin")
}
