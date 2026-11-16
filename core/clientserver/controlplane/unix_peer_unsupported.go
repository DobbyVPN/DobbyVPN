//go:build !linux && !darwin && !(windows || android || ios)

package controlplane

import (
	"errors"
	"net"
	"os"
)

func peerUID(net.Conn) (int, error) { return 0, errWrongLocalConnection }

var currentUID = func() int { return os.Getuid() }

func privilegedDefaultPeerUID() (int, error) {
	return 0, errors.New("privileged peer discovery is unsupported on this platform")
}

func authorizePrivilegedPeer(uid int) error {
	expected, err := privilegedPeerUID()
	if err != nil {
		return err
	}
	if uid != expected {
		return errPeerUIDNotInstalled
	}
	return nil
}

func privilegedListenOwnerUID() (int, error) { return privilegedPeerUID() }

func privilegedSocketModes() (os.FileMode, os.FileMode) { return 0o711, 0o600 }
