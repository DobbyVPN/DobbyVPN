//go:build windows

package trusttunnel

import (
	"errors"

	"golang.org/x/sys/windows"
)

func retryEndpointPairBind(err error) bool {
	return errors.Is(err, windows.WSAEADDRINUSE) || errors.Is(err, windows.WSAEACCES)
}
