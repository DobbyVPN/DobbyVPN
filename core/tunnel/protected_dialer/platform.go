package protected_dialer

import "errors"

// ErrSocketProtectionUnavailable means a non-loopback socket could not be
// excluded from the VPN route. Continuing in that situation can route the
// transport back into its own tunnel, so callers must treat it as fatal.
var ErrSocketProtectionUnavailable = errors.New("socket protection unavailable")

type platformProtector interface {
	Protect(fd uintptr, network string) error
}

var protector platformProtector
