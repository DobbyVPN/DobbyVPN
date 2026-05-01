//go:build !android && !ios
// +build !android,!ios

package exported_client

import "syscall"

func protector(string, string, syscall.RawConn) error {
	return nil
}
