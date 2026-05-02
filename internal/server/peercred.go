package server

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

var ErrPeerCredentialUnavailable = errors.New("peer credentials unavailable")

type peerCredentials struct {
	UID uint32
	GID uint32
	PID int32
}

func validatePeerCredentials(expectedUID uint32, credentials peerCredentials) error {
	if credentials.UID != expectedUID {
		return fmt.Errorf("peer uid %d does not match expected uid %d", credentials.UID, expectedUID)
	}

	return nil
}

func currentEffectiveUID() (uint32, error) {
	uid := os.Geteuid()
	if uid < 0 {
		return 0, fmt.Errorf("invalid effective uid %d", uid)
	}

	parsed, err := strconv.ParseUint(strconv.Itoa(uid), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse effective uid %d: %w", uid, err)
	}

	return uint32(parsed), nil
}
