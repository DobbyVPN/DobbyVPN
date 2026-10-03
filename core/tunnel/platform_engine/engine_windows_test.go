//go:build windows && !(android || ios)

package platform_engine

import (
	"context"
	"net"
	"regexp"
	"testing"
	"time"
	"unsafe"

	"github.com/xjasonlyu/tun2socks/v2/dialer"
	"golang.org/x/sys/windows"
)

func TestWindowsAdapterNamesAreUniqueAndOwnershipScoped(t *testing.T) {
	pattern := regexp.MustCompile(`^DobbyVPN-[0-9a-f]{32}$`)
	seen := make(map[string]bool)
	for range 64 {
		name, err := newWindowsAdapterName()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(name) {
			t.Fatalf("unexpected owned adapter name: %q", name)
		}
		if seen[name] {
			t.Fatalf("duplicate owned adapter name: %q", name)
		}
		seen[name] = true
	}
}

func TestWindowsAdapterRemovalWaitsForExactOwnedName(t *testing.T) {
	previous := listWindowsInterfaces
	t.Cleanup(func() { listWindowsInterfaces = previous })
	calls := 0
	listWindowsInterfaces = func() ([]net.Interface, error) {
		calls++
		if calls == 1 {
			return []net.Interface{{Name: "unrelated-wintun"}, {Name: "DobbyVPN-owned"}}, nil
		}
		return []net.Interface{{Name: "unrelated-wintun"}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitForWindowsAdapterRemoval(ctx, "DobbyVPN-owned"); err != nil {
		t.Fatal(err)
	}
	if calls < 2 {
		t.Fatalf("interface observations=%d, want at least 2", calls)
	}
}

func TestResetTun2SocksInterfaceBindingClearsPriorSession(t *testing.T) {
	dialer.DefaultDialer.InterfaceName.Store("Ethernet")
	dialer.DefaultDialer.InterfaceIndex.Store(17)
	t.Cleanup(resetTun2SocksInterfaceBinding)

	resetTun2SocksInterfaceBinding()
	if name := dialer.DefaultDialer.InterfaceName.Load(); name != "" {
		t.Fatalf("interface name=%q, want empty", name)
	}
	if index := dialer.DefaultDialer.InterfaceIndex.Load(); index != 0 {
		t.Fatalf("interface index=%d, want zero", index)
	}
}

func TestWindowsTunnelIPv4PrefixIsValidatedAndStable(t *testing.T) {
	prefix, err := windowsTunnelIPv4Prefix("10.0.0.2")
	if err != nil || prefix.String() != "10.0.0.2/24" {
		t.Fatalf("windowsTunnelIPv4Prefix() prefix=%s err=%v", prefix, err)
	}
	if _, err := windowsTunnelIPv4Prefix("not-an-ip"); err == nil {
		t.Fatal("windowsTunnelIPv4Prefix() accepted invalid address")
	}
	if _, err := windowsTunnelIPv4Prefix("2001:db8::2"); err == nil {
		t.Fatal("windowsTunnelIPv4Prefix() accepted IPv6 address")
	}
}

func TestWindowsDNSOriginUsesStaticNameServerOverride(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: "", want: false},
		{value: "  \t", want: false},
		{value: "1.1.1.1", want: true},
		{value: "1.1.1.1,8.8.8.8", want: true},
	} {
		if got := dnsNameServerIsStatic(test.value); got != test.want {
			t.Fatalf("dnsNameServerIsStatic(%q)=%t, want %t", test.value, got, test.want)
		}
	}
}

func TestFindInterfaceIPv4StateRequiresPreferredNonSkippedAddress(t *testing.T) {
	row := windows.MibUnicastIpAddressRow{
		InterfaceIndex: 17,
		DadState:       windows.IpDadStateTentative,
	}
	row.Address.Family = windows.AF_INET
	raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
	copy(raw.Addr[:], net.ParseIP("192.0.2.20").To4())

	found, preferred, skipped, duplicate, err := findInterfaceIPv4State(
		[]windows.MibUnicastIpAddressRow{row},
		17,
		net.ParseIP("192.0.2.20"),
	)
	if err != nil || !found || preferred || skipped || duplicate {
		t.Fatalf(
			"tentative state found=%t preferred=%t skipped=%t duplicate=%t err=%v",
			found,
			preferred,
			skipped,
			duplicate,
			err,
		)
	}

	row.DadState = windows.IpDadStatePreferred
	row.SkipAsSource = 0
	found, preferred, skipped, duplicate, err = findInterfaceIPv4State(
		[]windows.MibUnicastIpAddressRow{row},
		17,
		net.ParseIP("192.0.2.20"),
	)
	if err != nil || !found || !preferred || skipped || duplicate {
		t.Fatalf(
			"preferred state found=%t preferred=%t skipped=%t duplicate=%t err=%v",
			found,
			preferred,
			skipped,
			duplicate,
			err,
		)
	}
}
