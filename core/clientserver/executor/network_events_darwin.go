//go:build darwin && !ios

package executor

import (
	"context"
	"time"

	"golang.org/x/sys/unix"

	"core/log"
	"core/routing"
)

const (
	// kern.waketime is checked this often; it changes only when the system
	// wakes, so clock changes and scheduling delays cannot fake or hide one.
	wakeCheckInterval = 2 * time.Second
	// Right after a wake SystemConfiguration may not report the physical
	// network yet. Capture it again once a second, for at most this long.
	wakeNetworkBudget = 30 * time.Second
	wakeCapturePause  = time.Second
)

// watchWake reports a wake once the physical network is back, so a connected
// session checks its tunnel at once instead of on its next periodic check.
// macOS records each wake in kern.waketime; polling it needs no cgo, unlike
// IOKit power notifications, which need a run loop thread.
func watchWake(emit func(reason string)) {
	last, err := lastWakeTime()
	if err != nil {
		log.Errorf(desktopLogCategory, "wake watcher unavailable: %v", err)
		return
	}
	cancelPrevious := func() {}
	for range time.Tick(wakeCheckInterval) {
		current, err := lastWakeTime()
		if err != nil || current == last {
			continue
		}
		last = current
		log.Infof(desktopLogCategory, "system woke at %s", current.Format(time.RFC3339))
		// Only the newest wake waits for the network; an older wait ends
		// without reporting.
		cancelPrevious()
		ctx, cancel := context.WithCancel(context.Background())
		cancelPrevious = cancel
		go reportWakeWhenNetworkReady(ctx, emit, captureMacOSPhysicalNetwork, wakeNetworkBudget, wakeCapturePause)
	}
}

func lastWakeTime() (time.Time, error) {
	value, err := unix.SysctlTimeval("kern.waketime")
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(value.Sec, int64(value.Usec)*int64(time.Microsecond)), nil
}

func captureMacOSPhysicalNetwork(ctx context.Context) error {
	_, err := routing.CaptureMacOSPhysicalNetwork(ctx)
	return err
}

// reportWakeWhenNetworkReady retries the physical network capture that fails
// right after a wake for at most budget, then reports the wake. It reports
// even when the network does not return, so the tunnel check still runs and
// its failure is shown. A canceled parent means a newer wake took over.
func reportWakeWhenNetworkReady(parent context.Context, emit func(reason string), capture func(context.Context) error, budget, pause time.Duration) {
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	for attempt := 1; ; attempt++ {
		err := capture(ctx)
		if err == nil {
			log.Infof(desktopLogCategory, "physical network available after wake attempt=%d", attempt)
			break
		}
		select {
		case <-time.After(pause):
			continue
		case <-ctx.Done():
		}
		if parent.Err() != nil {
			return
		}
		log.Errorf(desktopLogCategory, "physical network still unavailable after wake attempts=%d: %v", attempt, err)
		break
	}
	if parent.Err() == nil {
		emit(eventWake)
	}
}

// Interface and address messages, and default route messages for a new
// gateway. Other route messages arrive constantly from neighbour-cache churn.
func watchNetworkChanges(emit func(reason string)) {
	go watchWake(emit)
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
