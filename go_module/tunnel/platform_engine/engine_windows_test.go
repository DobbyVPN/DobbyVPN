//go:build windows && !(android || ios)

package platform_engine

import (
	"net"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsAdapterNameIsExplicitAndStable(t *testing.T) {
	if WindowsAdapterName != "wintun" {
		t.Fatalf("unexpected owned adapter name: %q", WindowsAdapterName)
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
