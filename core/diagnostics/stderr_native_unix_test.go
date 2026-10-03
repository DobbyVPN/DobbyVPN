//go:build !windows

package diagnostics

import "golang.org/x/sys/unix"

func writeNativeStderr(data []byte) error {
	_, err := unix.Write(2, data)
	return err
}
