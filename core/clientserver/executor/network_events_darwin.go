//go:build darwin && !ios

package executor

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOMessage.h>
#include <IOKit/pwr_mgt/IOPMLib.h>
#include <unistd.h>

static io_connect_t dobby_power_root;
static int dobby_power_fd = -1;

static void dobby_power_callback(void *ref, io_service_t service, natural_t type, void *argument) {
	char event;
	switch (type) {
	case kIOMessageCanSystemSleep:
		IOAllowPowerChange(dobby_power_root, (long)argument);
		break;
	case kIOMessageSystemWillSleep:
		event = 's';
		(void)write(dobby_power_fd, &event, 1);
		IOAllowPowerChange(dobby_power_root, (long)argument);
		break;
	case kIOMessageSystemHasPoweredOn:
		event = 'w';
		(void)write(dobby_power_fd, &event, 1);
		break;
	}
}

// Registers for system power notifications and runs this thread's run loop.
// It returns only when the registration fails.
static int dobby_watch_system_power(int fd) {
	IONotificationPortRef port;
	io_object_t notifier;
	dobby_power_fd = fd;
	dobby_power_root = IORegisterForSystemPower(NULL, &port, dobby_power_callback, &notifier);
	if (dobby_power_root == MACH_PORT_NULL) {
		return -1;
	}
	CFRunLoopAddSource(CFRunLoopGetCurrent(), IONotificationPortGetRunLoopSource(port), kCFRunLoopDefaultMode);
	CFRunLoopRun();
	return 0;
}
*/
import "C"

import (
	"context"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"

	"core/log"
	"core/routing"
)

const (
	// Right after a wake SystemConfiguration may not report the physical
	// network yet. Capture it again once a second, for at most this long.
	wakeNetworkBudget = 30 * time.Second
	wakeCapturePause  = time.Second

	powerSleep = 's'
	powerWake  = 'w'
)

// watchWake reports a wake once the physical network is back, so a connected
// session checks its tunnel at once instead of on its next periodic check.
// The power notifications come from IOKit on a thread of their own and reach
// Go through a pipe.
func watchWake(emit func(reason string)) {
	read, write, err := os.Pipe()
	if err != nil {
		log.Errorf(desktopLogCategory, "wake watcher unavailable: %v", err)
		return
	}
	go func() {
		runtime.LockOSThread()
		if C.dobby_watch_system_power(C.int(write.Fd())) != 0 {
			log.Errorf(desktopLogCategory, "wake watcher unavailable: IORegisterForSystemPower failed")
			_ = write.Close()
		}
	}()
	waiter := &wakeWaiter{emit: emit, capture: captureMacOSPhysicalNetwork, budget: wakeNetworkBudget, pause: wakeCapturePause}
	event := make([]byte, 1)
	for {
		if _, err := read.Read(event); err != nil {
			log.Errorf(desktopLogCategory, "wake watcher stopped: %v", err)
			return
		}
		waiter.power(event[0])
	}
}

// wakeWaiter keeps one wait for the physical network at a time: a new wake
// or a sleep ends an older wait, which then reports nothing.
type wakeWaiter struct {
	emit          func(reason string)
	capture       func(context.Context) error
	budget, pause time.Duration
	cancel        context.CancelFunc
}

func (w *wakeWaiter) power(event byte) {
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
	if event != powerWake {
		log.Infof(desktopLogCategory, "system is going to sleep")
		return
	}
	log.Infof(desktopLogCategory, "system woke")
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go reportWakeWhenNetworkReady(ctx, w.emit, w.capture, w.budget, w.pause)
}

func captureMacOSPhysicalNetwork(ctx context.Context) error {
	_, err := routing.CaptureMacOSPhysicalNetwork(ctx)
	return err
}

// reportWakeWhenNetworkReady retries the physical network capture that fails
// right after a wake for at most budget, then reports the wake. It reports
// even when the network does not return, so the tunnel check still runs and
// its failure is shown. A canceled parent means a newer event took over.
func reportWakeWhenNetworkReady(parent context.Context, emit func(reason string), capture func(context.Context) error, budget, pause time.Duration) {
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	for attempt := 1; ; attempt++ {
		// SystemConfiguration calls may not honour ctx; the wait is bounded
		// by the budget even if a capture blocks, and its late result is
		// dropped.
		result := make(chan error, 1)
		go func() { result <- capture(ctx) }()
		var err error
		select {
		case err = <-result:
		case <-ctx.Done():
			err = ctx.Err()
		}
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
