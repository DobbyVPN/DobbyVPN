//go:build !windows

package trusttunnel

import (
	"errors"
	"syscall"
)

func retryEndpointPairBind(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.EACCES)
}
