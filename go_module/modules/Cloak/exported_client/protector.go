//go:build !android
// +build !android

package exported_client

import "syscall"

func protector(string, string, syscall.RawConn) error {
	return nil
}
