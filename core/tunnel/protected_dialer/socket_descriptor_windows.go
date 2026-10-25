//go:build windows && !(android || ios)

package protected_dialer

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const soProtocolInfoW = 0x2005

func socketNetworkFromDescriptor(fd int) (string, error) {
	var info windows.WSAProtocolInfo
	length := int32(unsafe.Sizeof(info))
	if err := windows.Getsockopt(
		windows.Handle(uintptr(fd)),
		windows.SOL_SOCKET,
		soProtocolInfoW,
		(*byte)(unsafe.Pointer(&info)),
		&length,
	); err != nil {
		return "", fmt.Errorf("getsockopt SO_PROTOCOL_INFOW: %w", err)
	}
	if length != int32(unsafe.Sizeof(info)) {
		return "", fmt.Errorf("unexpected SO_PROTOCOL_INFOW size %d, want %d", length, unsafe.Sizeof(info))
	}
	return socketNetwork(int(info.AddressFamily), int(info.SocketType))
}
