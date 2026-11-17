//go:build darwin

package executor

import (
	"golang.org/x/sys/unix"

	"core/log"
)

// Interface and address messages only: plain route messages arrive constantly
// from neighbour-cache churn and are not a network change.
func watchNetworkChanges(emit func(reason string)) {
	fd, err := unix.Socket(unix.AF_ROUTE, unix.SOCK_RAW, 0)
	if err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	buf := make([]byte, 1<<16)
	for {
		n, err := unix.Read(fd, buf)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Errorf(desktopLogCategory, "network change watcher stopped: %v", err)
			return
		}
		if isNetworkChangeMessage(buf[:n]) {
			emit(eventNetworkChanged)
		}
	}
}

// A routing message starts with msglen (2 bytes), version and type.
func isNetworkChangeMessage(message []byte) bool {
	if len(message) < 4 {
		return false
	}
	switch message[3] {
	case unix.RTM_IFINFO, unix.RTM_IFINFO2, unix.RTM_NEWADDR, unix.RTM_DELADDR:
		return true
	}
	return false
}
