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

// The destination, gateway and netmask sockaddrs follow the header in that
// order, each padded to 4 bytes. Only a zero destination with a zero-length
// mask is a default route: DobbyVPN's own 0.0.0.0/1 and ::/1 are not.
func isDefaultDestination(message []byte) bool {
	if len(message) < unix.SizeofRtMsghdr {
		return false
	}
	addrs := message[12]
	var destination, mask []byte
	offset := unix.SizeofRtMsghdr
	for _, bit := range []byte{unix.RTA_DST, unix.RTA_GATEWAY, unix.RTA_NETMASK} {
		if addrs&bit == 0 {
			continue
		}
		if offset >= len(message) {
			return false
		}
		size := 4
		if length := int(message[offset]); length > 0 {
			size = (length + 3) &^ 3
		}
		end := min(offset+size, len(message))
		switch bit {
		case unix.RTA_DST:
			destination = message[offset:end]
		case unix.RTA_NETMASK:
			mask = message[offset:end]
		}
		offset += size
	}
	if !zeroAddress(destination) {
		return false
	}
	if mask == nil {
		return message[8]&unix.RTF_HOST == 0
	}
	return allZero(mask[min(2, len(mask)):])
}

func zeroAddress(sockaddr []byte) bool {
	switch {
	case len(sockaddr) >= 8 && sockaddr[1] == unix.AF_INET:
		return allZero(sockaddr[4:8])
	case len(sockaddr) >= 24 && sockaddr[1] == unix.AF_INET6:
		return allZero(sockaddr[8:24])
	}
	return false
}

func allZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}
