//go:build !linux && !android

package internal

import "fmt"

func bindPlatformUDPAddress(uintptr, []byte, uint32) error {
	return fmt.Errorf("UDP source binding is not implemented on this platform")
}
