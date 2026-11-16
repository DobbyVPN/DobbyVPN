//go:build darwin

package controlplane

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

func peerUID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errWrongLocalConnection
	}
	var uid int
	var controlErr error
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			controlErr = err
			return
		}
		uid = int(cred.Uid)
	})
	if err != nil {
		return 0, err
	}
	if controlErr != nil {
		return 0, controlErr
	}
	return uid, nil
}

var currentUID = os.Getuid

func privilegedDefaultPeerUID() (int, error) {
	return 0, errors.New("the macOS installer must configure its desktop UID")
}

// authorizePrivilegedPeer requires the pinned console UID the macOS installer sets.
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

// privilegedListenOwnerUID keeps the macOS socket owned by the installer's UID.
func privilegedListenOwnerUID() (int, error) { return privilegedPeerUID() }

func privilegedSocketModes() (dirMode, socketMode os.FileMode) { return 0o711, 0o600 }
