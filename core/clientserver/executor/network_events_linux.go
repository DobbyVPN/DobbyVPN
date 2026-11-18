//go:build linux && !android

package executor

import (
	"syscall"

	"golang.org/x/sys/unix"

	"core/log"
)

// Link and address changes cover a new network, a lost link and the network
// coming back after a wake-up; default route changes cover a new gateway.
func watchNetworkChanges(emit func(reason string)) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	groups := uint32(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR | unix.RTMGRP_IPV4_ROUTE | unix.RTMGRP_IPV6_ROUTE)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: groups}); err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
		return
	}
	buf := make([]byte, 1<<16)
	for {
		n, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Errorf(desktopLogCategory, "network change watcher stopped: %v", err)
			return
		}
		if isNetworkChange(buf[:n]) {
			emit(eventNetworkChanged)
		}
	}
}

// Route messages count only for the default route (destination length 0);
// other routes change constantly and are not a network change.
func isNetworkChange(data []byte) bool {
	messages, err := syscall.ParseNetlinkMessage(data)
	if err != nil {
		return true
	}
	for _, message := range messages {
		switch message.Header.Type {
		case syscall.RTM_NEWROUTE, syscall.RTM_DELROUTE:
			if len(message.Data) > 1 && message.Data[1] == 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}
