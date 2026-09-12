//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func sourceFileReadMayFallbackToInline(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, windows.ERROR_INVALID_NAME)
}
