//go:build linux

package controlplane

import (
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
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
		cred, credErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if credErr != nil {
			controlErr = credErr
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

var currentUID = func() int { return os.Getuid() }

// privilegedDefaultPeerUID is unused on Linux: the service authorizes regular
// local accounts per connection and never pins a desktop UID.
func privilegedDefaultPeerUID() (int, error) {
	return 0, errors.New("privileged peer discovery is unused on Linux")
}

// authorizePrivilegedPeer admits root and resolvable regular local accounts so a
// headless service serves the machine without a logged-in desktop session.
func authorizePrivilegedPeer(uid int) error {
	return authorizeRegularUser(uid, lookupUserID)
}

// privilegedListenOwnerUID leaves a privileged socket root-owned; every
// connection is authenticated by peer credentials instead.
func privilegedListenOwnerUID() (int, error) { return -1, nil }

func privilegedSocketModes() (os.FileMode, os.FileMode) { return 0o755, 0o666 }
