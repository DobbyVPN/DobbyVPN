package sessionapi

import (
	"golang.org/x/sys/windows"
	"syscall"
)

// Winsock returns native WSA codes in net.OpError.
func init() {
	for _, errno := range []syscall.Errno{
		windows.WSAENETDOWN, windows.WSAENETUNREACH, windows.WSAEHOSTUNREACH,
		windows.WSAECONNREFUSED, windows.WSAETIMEDOUT,
	} {
		offlineSocketErrors[errno] = true
	}
}
