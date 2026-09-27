package platform_engine

import (
	"errors"
	"fmt"
	"net"
	"sort"

	"go_module/routing"
)

// recoverStaleWindowsResources cleans only DobbyVPN's exact Windows ownership
// namespace. Routing is cleared before any adapter is removed so every owned
// route can still be identified and verified against its interface.
func recoverStaleWindowsResources(
	interfaces []net.Interface,
	cleanupRoutes func(string) error,
	cleanupIPv6Rules func() error,
	removeAdapter func(string) error,
) error {
	if cleanupRoutes == nil || cleanupIPv6Rules == nil || removeAdapter == nil {
		return errors.New("Windows stale-resource recovery requires all cleanup operations")
	}

	owned := make([]string, 0, len(interfaces))
	seen := make(map[string]struct{}, len(interfaces))
	for _, iface := range interfaces {
		if !routing.IsOwnedWindowsTunnelInterface(iface.Name) {
			continue
		}
		if _, duplicate := seen[iface.Name]; duplicate {
			continue
		}
		seen[iface.Name] = struct{}{}
		owned = append(owned, iface.Name)
	}
	sort.Strings(owned)

	var errs []error
	routesClean := make(map[string]bool, len(owned))
	for _, name := range owned {
		if err := cleanupRoutes(name); err != nil {
			errs = append(errs, fmt.Errorf("clean stale routes on owned adapter %q: %w", name, err))
			continue
		}
		routesClean[name] = true
	}
	if err := cleanupIPv6Rules(); err != nil {
		errs = append(errs, fmt.Errorf("clean stale owned IPv6 firewall rules: %w", err))
	}
	if len(errs) != 0 {
		return errors.Join(errs...)
	}

	for _, name := range owned {
		if !routesClean[name] {
			continue
		}
		if err := removeAdapter(name); err != nil {
			errs = append(errs, fmt.Errorf("remove stale owned adapter %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}
