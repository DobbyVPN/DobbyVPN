//go:build !linux && !android

package internal

import (
	"fmt"

	"github.com/xtls/xray-core/transport/internet"
)

func applyPlatformOutboundSocketOptions(string, string, uintptr, *internet.SocketConfig) error {
	return nil
}

func bindPlatformUDPAddress(uintptr, []byte, uint32) error {
	return fmt.Errorf("UDP source binding is not implemented on this platform")
}
