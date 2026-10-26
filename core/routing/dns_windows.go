//go:build windows

package routing

import (
	"errors"
	"fmt"
	"net/netip"

	"core/log"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// SetWindowsDNS updates only the owned tunnel adapter. The pinned Windows
// adapter library applies each address family through IP Helper and supports
// the existing minimum Windows version. An empty list restores automatic DNS.
func SetWindowsDNS(luid winipcfg.LUID, static bool, servers []netip.Addr) error {
	if !static {
		servers = nil
	}
	var errs []error
	for _, family := range []winipcfg.AddressFamily{windows.AF_INET, windows.AF_INET6} {
		if err := luid.SetDNS(family, servers, nil); err != nil {
			errs = append(errs, fmt.Errorf("set tunnel DNS luid=%d family=%d: %w", luid, family, err))
		} else {
			log.Debugf(Category, "set tunnel DNS luid=%d family=%d static=%t servers=%v", luid, family, static, servers)
		}
	}
	return errors.Join(errs...)
}
