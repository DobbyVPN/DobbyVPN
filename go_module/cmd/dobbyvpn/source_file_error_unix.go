//go:build !windows

package main

import "os"

func sourceFileReadMayFallbackToInline(err error) bool {
	return os.IsNotExist(err)
}
