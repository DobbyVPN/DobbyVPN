//go:build windows && !(android || ios)

package platform_engine

import (
	"net"
	"net/netip"
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

func TestWindowsEngineKeyLeavesLoopbackUDPRelayUnbound(t *testing.T) {
	key := windowsEngineKey(EngineConfig{ProxyAddr: "127.0.0.1:1080", UplinkIface: "Ethernet"}, "DobbyVPN-test")
	if key.Interface != "" {
		t.Fatalf("engine key interface=%q, want empty so the local UDP relay can reach loopback", key.Interface)
	}
	if key.Proxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("engine key proxy=%q", key.Proxy)
	}
	if key.Device != "DobbyVPN-test" {
		t.Fatalf("engine key device=%q", key.Device)
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
	if err := waitForWindowsAdapterRemoval("DobbyVPN-owned", time.Second); err != nil {
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

func TestWindowsDADCommandsAreActiveStoreOnly(t *testing.T) {
	if got, want := windowsSetDADCommand("wintun", 0), `netsh interface ipv4 set interface interface="wintun" dadtransmits=0 store=active`; got != want {
		t.Fatalf("disable DAD command=%q, want=%q", got, want)
	}
	if got, want := windowsSetDADCommand("wintun", 3), `netsh interface ipv4 set interface interface="wintun" dadtransmits=3 store=active`; got != want {
		t.Fatalf("restore DAD command=%q, want=%q", got, want)
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

func TestWindowsDNSCommandsOnlyChangeServerList(t *testing.T) {
	server := netip.MustParseAddr("1.1.1.1")
	if got := windowsSetDNSCommand("wintun", server); got != `netsh interface ipv4 set dnsservers name="wintun" static 1.1.1.1 primary` {
		t.Fatalf("set DNS command=%q", got)
	}
	if got := windowsClearDNSCommand("wintun"); got != `netsh interface ipv4 delete dnsservers name="wintun" all` {
		t.Fatalf("empty DNS restore command=%q", got)
	}
	if got := windowsAddDNSCommand("wintun", server, 2); got != `netsh interface ipv4 add dnsservers name="wintun" 1.1.1.1 index=2` {
		t.Fatalf("additional DNS restore command=%q", got)
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
