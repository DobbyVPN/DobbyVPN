//go:build linux && !android

package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"

	"core/sessionapi"

	"golang.org/x/sys/unix"
)

// AcquireLinuxResolvedDNS makes systemd-resolved send all DNS through this
// session's TUN. The TUN is newly created for the session, so reverting its
// per-link state restores the exact baseline (no state) without touching the
// uplink resolver configuration.
func (p *Plan) AcquireLinuxResolvedDNS(ctx context.Context, tunName string, dnsIPs []string) (*Lease, error) {
	if tunName == "" || strings.ContainsAny(tunName, " \t\r\n") {
		return nil, fmt.Errorf("invalid Linux DNS interface %q", tunName)
	}
	addresses := make([]resolvedAddress, 0, len(dnsIPs))
	for _, value := range dnsIPs {
		ip := net.ParseIP(value)
		if ip == nil {
			return nil, fmt.Errorf("invalid Linux DNS server %q", value)
		}
		family, address := int32(unix.AF_INET6), ip.To16()
		if ip.To4() != nil {
			family, address = unix.AF_INET, ip.To4()
		}
		addresses = append(addresses, resolvedAddress{Family: family, Address: []byte(address)})
	}
	if len(addresses) == 0 {
		return nil, errors.New("VPN DNS list is empty")
	}
	index, lookupErr := linuxLinkIndex(tunName)
	if lookupErr != nil {
		return nil, lookupErr
	}
	if index <= 0 || index > math.MaxInt32 {
		return nil, fmt.Errorf("invalid resolved interface index %d", index)
	}
	linkIndex := int32(index)
	revert := func(cleanupCtx context.Context) error {
		current, err := linuxLinkIndex(tunName)
		if err != nil {
			return fmt.Errorf("verify resolved link ownership: %w", err)
		}
		if current != index {
			return fmt.Errorf("resolved interface %s changed index from %d to %d", tunName, index, current)
		}
		return linuxResolvedCall(cleanupCtx, "RevertLink", linkIndex)
	}

	// Record ownership before the first call: even a failed D-Bus exchange can
	// have changed the OS state before its reply was lost.
	lease, err := p.Acquire("resolved-dns "+tunName, func() error { return nil }, revert)
	if err != nil {
		return nil, err
	}
	for _, call := range []struct {
		method string
		args   []any
	}{
		{"SetLinkDNS", []any{linkIndex, addresses}},
		{"SetLinkDomains", []any{linkIndex, []resolvedDomain{{Domain: ".", RoutingOnly: true}}}},
		{"SetLinkDefaultRoute", []any{linkIndex, true}},
	} {
		if err := linuxResolvedCall(ctx, call.method, call.args...); err != nil {
			if cleanupErr := lease.Close(sessionapi.CleanupContext(ctx)); cleanupErr != nil {
				return lease, &sessionapi.CleanupFailure{Err: errors.Join(err, cleanupErr)}
			}
			return nil, err
		}
	}
	return lease, nil
}
