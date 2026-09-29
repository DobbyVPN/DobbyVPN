//go:build linux

package controlplane

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
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
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
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

// A boot-time system service has no inherited desktop environment. Permit the
// sole logged-in desktop UID under /run/user; multiple sessions fail closed.
func privilegedDefaultPeerUID() (int, error) {
	entries, err := os.ReadDir("/run/user")
	if err != nil {
		return 0, err
	}
	candidates := make([]int, 0, 1)
	for _, entry := range entries {
		uid, err := strconv.Atoi(entry.Name())
		if err != nil || uid < 1000 || uid >= 60000 {
			continue
		}
		info, err := os.Stat(filepath.Join("/run/user", entry.Name()))
		if err != nil || !info.IsDir() {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != uid {
			continue
		}
		candidates = append(candidates, uid)
	}
	if len(candidates) != 1 {
		return 0, fmt.Errorf("expected one logged-in desktop UID, found %d", len(candidates))
	}
	return candidates[0], nil
}
