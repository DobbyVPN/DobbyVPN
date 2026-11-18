//go:build windows

package routing

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"sync/atomic"
	"unsafe"

	"core/log"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

var ownedWindowsLUID atomic.Uint64
var getBestRoute2 = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetBestRoute2")

// SetWindowsOwnedLUID identifies the app-owned adapter until its removal is confirmed.
func SetWindowsOwnedLUID(luid uint64) { ownedWindowsLUID.Store(luid) }

func windowsBestRoute(destination netip.Addr) (winipcfg.MibIPforwardRow2, error) {
	var addr, source winipcfg.RawSockaddrInet
	var row winipcfg.MibIPforwardRow2
	if err := addr.SetAddr(destination); err != nil {
		return row, err
	}
	status, _, _ := getBestRoute2.Call(0, 0, 0, uintptr(unsafe.Pointer(&addr)), 0, uintptr(unsafe.Pointer(&row)), uintptr(unsafe.Pointer(&source)))
	if status != 0 {
		return row, windows.Errno(status)
	}
	return row, nil
}

func normalizeWindowsScope(destination netip.Addr, lookup func(string) (int, error)) (netip.Addr, uint64, error) {
	zone := destination.Zone()
	if zone == "" {
		return destination, 0, nil
	}
	index, err := strconv.ParseUint(zone, 10, 32)
	if err != nil {
		n, lookupErr := lookup(zone)
		if lookupErr != nil {
			return netip.Addr{}, 0, lookupErr
		}
		if n <= 0 {
			return netip.Addr{}, 0, fmt.Errorf("invalid interface scope %q", zone)
		}
		index = uint64(n)
	}
	if index == 0 {
		return netip.Addr{}, 0, fmt.Errorf("invalid interface scope %q", zone)
	}
	return destination.WithZone(strconv.FormatUint(index, 10)), index, nil
}

// WindowsUnderlayForDestination selects a route for the numeric destination.
func WindowsUnderlayForDestination(destination netip.Addr, preferredLUID uint64) (UnderlayRoute, error) {
	destination = destination.Unmap()
	if !destination.IsValid() {
		return UnderlayRoute{}, fmt.Errorf("invalid destination")
	}
	family := winipcfg.AddressFamily(windows.AF_INET6)
	if destination.Is4() {
		family = windows.AF_INET
	}
	destination, scopeIndex, err := normalizeWindowsScope(destination, func(name string) (int, error) {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return 0, err
		}
		return iface.Index, nil
	})
	if err != nil {
		return UnderlayRoute{}, err
	}
	matchDestination := destination.WithZone("")
	rows, err := winipcfg.GetIPForwardTable2(family)
	if err != nil {
		return UnderlayRoute{}, err
	}
	best, bestErr := windowsBestRoute(destination)
	if bestErr != nil {
		return UnderlayRoute{}, fmt.Errorf("query best Windows route for %s: %w", destination, bestErr)
	}
	candidates := make([]UnderlayRoute, 0, len(rows))
	var osLUID uint64
	var osRoute UnderlayRoute
	for _, row := range rows {
		prefix := row.DestinationPrefix.Prefix()
		if scopeIndex != 0 && uint64(row.InterfaceIndex) != scopeIndex {
			continue
		}
		iface, err := net.InterfaceByIndex(int(row.InterfaceIndex))
		if err != nil {
			continue
		}
		actual, err := winipcfg.LUIDFromIndex(row.InterfaceIndex)
		if err != nil {
			continue
		}
		if !EligibleUnderlay(prefix, matchDestination, uint64(row.InterfaceLUID), row.InterfaceIndex, uint64(actual), ownedWindowsLUID.Load(), iface.Flags&net.FlagUp != 0, row.Loopback || iface.Flags&net.FlagLoopback != 0, row.Immortal || row.ValidLifetime != 0) {
			continue
		}
		settings, err := row.InterfaceLUID.IPInterface(family)
		if err != nil {
			continue
		}
		guid, err := row.InterfaceLUID.GUID()
		if err != nil {
			continue
		}
		candidate := UnderlayRoute{Prefix: prefix, NextHop: row.NextHop.Addr(), LUID: uint64(row.InterfaceLUID), Index: row.InterfaceIndex, GUID: guid.String(), RouteMetric: row.Metric, InterfaceMetric: settings.Metric}
		candidates = append(candidates, candidate)
		if bestErr == nil && best.InterfaceLUID == row.InterfaceLUID && best.InterfaceIndex == row.InterfaceIndex && best.DestinationPrefix.Prefix() == prefix && best.NextHop.Addr() == row.NextHop.Addr() {
			osLUID = candidate.LUID
			osRoute = candidate
		}
	}
	if osLUID == 0 && (ownedWindowsLUID.Load() == 0 || uint64(best.InterfaceLUID) != ownedWindowsLUID.Load()) {
		return UnderlayRoute{}, fmt.Errorf("best Windows route for %s is no longer eligible", destination)
	}
	selected := osRoute
	if osLUID == 0 {
		selected, err = SelectUnderlay(matchDestination, candidates, 0, preferredLUID)
	}
	if err != nil {
		return selected, fmt.Errorf("select Windows underlay for %s (best route: %v): %w", destination, bestErr, err)
	}
	log.Infof(Category, "Windows underlay destination=%s prefix=%s luid=%d index=%d guid=%s route_metric=%d interface_metric=%d os_best=%t", destination, selected.Prefix, selected.LUID, selected.Index, selected.GUID, selected.RouteMetric, selected.InterfaceMetric, osLUID != 0)
	return selected, nil
}

// WindowsUnderlayIdentity resolves a currently bound socket index, ignoring
// stale, loopback, down and owned identities before using it as a tie preference.
func WindowsUnderlayIdentity(index uint32) (UnderlayRoute, error) {
	luid, err := winipcfg.LUIDFromIndex(index)
	if err != nil {
		return UnderlayRoute{}, err
	}
	guid, err := luid.GUID()
	if err != nil {
		return UnderlayRoute{}, err
	}
	route := UnderlayRoute{LUID: uint64(luid), Index: index, GUID: guid.String()}
	if err := ValidateWindowsUnderlay(route); err != nil {
		return UnderlayRoute{}, err
	}
	return route, nil
}

// ValidateWindowsUnderlay detects index reuse and ownership changes before bind.
func ValidateWindowsUnderlay(route UnderlayRoute) error {
	if route.LUID == 0 || route.LUID == ownedWindowsLUID.Load() {
		return fmt.Errorf("underlay LUID is unavailable or owned by this VPN")
	}
	actual, err := winipcfg.LUIDFromIndex(route.Index)
	if err != nil {
		return err
	}
	if uint64(actual) != route.LUID {
		return fmt.Errorf("underlay index %d changed LUID", route.Index)
	}
	iface, err := net.InterfaceByIndex(int(route.Index))
	if err != nil {
		return err
	}
	if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
		return fmt.Errorf("underlay interface %d is not eligible", route.Index)
	}
	guid, err := actual.GUID()
	if err != nil {
		return err
	}
	if guid.String() != route.GUID {
		return fmt.Errorf("underlay interface %d changed GUID", route.Index)
	}
	return nil
}
