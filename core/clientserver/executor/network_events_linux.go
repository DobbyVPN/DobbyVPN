//go:build linux

package executor

import (
	"golang.org/x/sys/unix"

	"core/log"
)

// Link and address changes cover a new network, a lost link and the network
// coming back after a wake-up.
func watchNetworkChanges(emit func(reason string)) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_ROUTE)
	if err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
		return
	}
	defer func() { _ = unix.Close(fd) }()
	groups := uint32(unix.RTMGRP_LINK | unix.RTMGRP_IPV4_IFADDR | unix.RTMGRP_IPV6_IFADDR)
	if err := unix.Bind(fd, &unix.SockaddrNetlink{Family: unix.AF_NETLINK, Groups: groups}); err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
		return
	}
	buf := make([]byte, 1<<16)
	for {
		if _, _, err := unix.Recvfrom(fd, buf, 0); err != nil {
			if err == unix.EINTR {
				continue
			}
			log.Errorf(desktopLogCategory, "network change watcher stopped: %v", err)
			return
		}
		emit(eventNetworkChanged)
	}
}
