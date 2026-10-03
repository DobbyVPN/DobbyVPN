//go:build windows

package routing

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"

	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

func routeFixture(t *testing.T) (*winipcfg.MibIPforwardRow2, **winipcfg.MibIPforwardRow2, *int) {
	t.Helper()
	oldIdentity, oldRead, oldCreate, oldDelete := windowsRouteIdentity, windowsRouteRead, windowsRouteCreate, windowsRouteDelete
	t.Cleanup(func() {
		windowsRouteIdentity, windowsRouteRead, windowsRouteCreate, windowsRouteDelete = oldIdentity, oldRead, oldCreate, oldDelete
	})
	key := &winipcfg.MibIPforwardRow2{InterfaceLUID: 71, InterfaceIndex: 9}
	if err := key.DestinationPrefix.SetPrefix(netip.MustParsePrefix("198.51.100.7/32")); err != nil {
		t.Fatal(err)
	}
	if err := key.NextHop.SetAddr(netip.MustParseAddr("192.0.2.1")); err != nil {
		t.Fatal(err)
	}
	var current *winipcfg.MibIPforwardRow2
	deletes := 0
	windowsRouteIdentity = func(windowsRoute) (*winipcfg.MibIPforwardRow2, error) { return key, nil }
	windowsRouteRead = func(got *winipcfg.MibIPforwardRow2) (*winipcfg.MibIPforwardRow2, error) {
		if got.InterfaceLUID != 71 || got.InterfaceIndex != 9 {
			t.Fatalf("lost adapter identity: %+v", got)
		}
		if current == nil {
			return nil, nil
		}
		copy := *current
		return &copy, nil
	}
	windowsRouteCreate = func(row *winipcfg.MibIPforwardRow2) error { copy := *row; current = &copy; return nil }
	windowsRouteDelete = func(row *winipcfg.MibIPforwardRow2) error { deletes++; current = nil; return nil }
	return key, &current, &deletes
}

func TestWindowsRoutePreservesForeignAndDeletesOnlyItsOwn(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "foreign"}[foreign], func(t *testing.T) {
			key, current, deletes := routeFixture(t)
			if foreign {
				*current = key
			}
			plan := NewPlan("test")
			changed, err := AcquireProxyRoute(plan, "198.51.100.7", "192.0.2.1", "Ethernet")
			if err != nil || changed == foreign {
				t.Fatalf("changed=%v error=%v", changed, err)
			}
			if err := plan.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			if (*deletes == 0) != foreign {
				t.Fatalf("deletions=%d foreign=%v", *deletes, foreign)
			}
		})
	}
}

func TestWindowsRouteRetainsReplacementAndOriginalErrors(t *testing.T) {
	_, current, deletes := routeFixture(t)
	plan := NewPlan("test")
	if _, err := AcquireProxyRoute(plan, "198.51.100.7", "192.0.2.1", "Ethernet"); err != nil {
		t.Fatal(err)
	}
	(*current).Metric++
	if err := plan.Close(context.Background()); err == nil {
		t.Fatal("replacement was accepted as owned")
	}
	if *deletes != 0 {
		t.Fatal("foreign replacement deleted")
	}
	(*current).Metric--
	want := errors.New("IP Helper access denied")
	windowsRouteDelete = func(*winipcfg.MibIPforwardRow2) error { return want }
	if err := plan.Close(context.Background()); !errors.Is(err, want) {
		t.Fatalf("lost original error: %v", err)
	}
	windowsRouteDelete = func(*winipcfg.MibIPforwardRow2) error { *current = nil; return nil }
	if err := plan.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRouteRejectsUnconfirmedDeletion(t *testing.T) {
	key, current, _ := routeFixture(t)
	*current = key
	windowsRouteDelete = func(*winipcfg.MibIPforwardRow2) error { return nil }
	if err := releaseWindowsRoute(key); err == nil {
		t.Fatal("reported cleanup with remaining route")
	}
}

func TestSelectExactInterfaceNeverUsesSubstringMatch(t *testing.T) {
	interfaces := []net.Interface{{Name: "other-wintun"}, {Name: "DobbyVPN-owned"}}
	iface, err := selectExactInterface("DobbyVPN-owned", interfaces)
	if err != nil || iface.Name != "DobbyVPN-owned" {
		t.Fatalf("iface=%v err=%v", iface, err)
	}
	if _, err := selectExactInterface("wintun", interfaces); err == nil {
		t.Fatal("substring selected foreign interface")
	}
}

func TestWindowsFirewallRangeDoesNotMatchIPv4OrPartialIPv6(t *testing.T) {
	for _, value := range []string{windowsIPv6Range, "::-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "::/0"} {
		if !isWindowsIPv6Range(value) {
			t.Errorf("IPv6 range not recognized: %s", value)
		}
	}
	for _, value := range []string{"*", "0.0.0.0/0", "::/1", "::/0,0.0.0.0/0", "LocalSubnet"} {
		if isWindowsIPv6Range(value) {
			t.Errorf("foreign policy recognized as owned: %s", value)
		}
	}
}
