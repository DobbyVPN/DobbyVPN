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
func currentUID() int { return os.Getuid() }

func privilegedDefaultPeerUID() (int, error) {
	return 0, errors.New("the macOS installer must configure its desktop UID")
}
