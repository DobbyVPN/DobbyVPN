//go:build darwin && !ios

package executor

import (
	"golang.org/x/sys/unix"

	"core/log"
)

// Interface and address messages, and default route messages for a new
// gateway. Other route messages arrive constantly from neighbour-cache churn.
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

// A routing message starts with msglen (2 bytes), version and type; a route
// message carries its destination sockaddr right after the header.
func isNetworkChangeMessage(message []byte) bool {
	if len(message) < 4 {
		return false
	}
	switch message[3] {
	case unix.RTM_IFINFO, unix.RTM_IFINFO2, unix.RTM_NEWADDR, unix.RTM_DELADDR:
		return true
	case unix.RTM_ADD, unix.RTM_DELETE, unix.RTM_CHANGE:
		return isDefaultDestination(message)
	}
	return false
}

func isDefaultDestination(message []byte) bool {
	const header = unix.SizeofRtMsghdr
	if len(message) < header+2 || message[12]&unix.RTA_DST == 0 {
		return false
	}
	var address []byte
	switch message[header+1] {
	case unix.AF_INET:
		if len(message) >= header+8 {
			address = message[header+4 : header+8]
		}
	case unix.AF_INET6:
		if len(message) >= header+24 {
			address = message[header+8 : header+24]
		}
	}
	if address == nil {
		return false
	}
	for _, b := range address {
		if b != 0 {
			return false
		}
	}
	return true
}
