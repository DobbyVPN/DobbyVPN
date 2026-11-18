//go:build windows

package executor

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"core/log"
)

// Power broadcast event types of SERVICE_CONTROL_POWEREVENT (winuser.h).
const (
	powerSuspend         = 0x4
	powerResumeSuspend   = 0x7
	powerResumeAutomatic = 0x12
)

// powerEvent logs a suspend and reports a resume as a wake. Both resume types
// can arrive for one wake-up; the debounce joins them.
func powerEvent(eventType uint32, emit func(reason string)) {
	switch eventType {
	case powerSuspend:
		log.Infof(desktopLogCategory, "system event: sleep")
	case powerResumeSuspend, powerResumeAutomatic:
		emit(eventWake)
	}
}

// watchNetworkChanges reports unicast address changes until stop is called.
// The callback runs on a system thread and only queues the event.
func watchNetworkChanges(emit func(reason string)) (stop func(), err error) {
	callback := syscall.NewCallback(func(_, _ uintptr, _ uint32) uintptr {
		emit(eventNetworkChanged)
		return 0
	})
	var handle windows.Handle
	if err := windows.NotifyUnicastIpAddressChange(windows.AF_UNSPEC, callback, unsafe.Pointer(nil), false, &handle); err != nil {
		return nil, fmt.Errorf("watch address changes: %w", err)
	}
	return func() { _ = windows.CancelMibChangeNotify2(handle) }, nil
}
