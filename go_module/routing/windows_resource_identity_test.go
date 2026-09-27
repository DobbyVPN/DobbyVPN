package routing

import "testing"

func TestIsOwnedWindowsTunnelInterfaceRequiresExactGeneratedAlias(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"DobbyVPN-0123456789abcdef0123456789abcdef", true},
		{"DobbyVPN-0123456789abcdef0123456789abcdeF", false},
		{"DobbyVPN-0123456789abcdef0123456789abcde", false},
		{"DobbyVPN-0123456789abcdef0123456789abcdef-extra", false},
		{"other-0123456789abcdef0123456789abcdef", false},
		{"DobbyVPN-wintun", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsOwnedWindowsTunnelInterface(test.name); got != test.want {
				t.Fatalf("IsOwnedWindowsTunnelInterface(%q)=%t, want %t", test.name, got, test.want)
			}
		})
	}
}

func TestIsOwnedWindowsTunRedirectRequiresExactRouteAndAlias(t *testing.T) {
	owned := "DobbyVPN-0123456789abcdef0123456789abcdef"
	for _, test := range []struct {
		prefix        string
		nextHop       string
		interfaceName string
		want          bool
	}{
		{"0.0.0.0/1", "0.0.0.0", owned, true},
		{"128.0.0.0/1", "0.0.0.0", owned, true},
		{"0.0.0.0/0", "0.0.0.0", owned, false},
		{"0.0.0.0/1", "192.0.2.1", owned, false},
		{"0.0.0.0/1", "0.0.0.0", "Ethernet", false},
	} {
		if got := IsOwnedWindowsTunRedirect(test.prefix, test.nextHop, test.interfaceName); got != test.want {
			t.Errorf("IsOwnedWindowsTunRedirect(%q, %q, %q)=%t, want %t", test.prefix, test.nextHop, test.interfaceName, got, test.want)
		}
	}
}

func TestIsOwnedWindowsIPv6RuleNameRequiresGeneratedSessionID(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{"DobbyVPN Block IPv6 windows-1790525851930782900-2", true},
		{"DobbyVPN Block IPv6 windows-1-1", true},
		{"DobbyVPN Block IPv6 windows--1", false},
		{"DobbyVPN Block IPv6 windows-1-x", false},
		{"DobbyVPN Block IPv6 windows-1-1-extra", false},
		{"DobbyVPN Block IPv6 windows-1-1 ", false},
		{"DobbyVPN Block IPv6 darwin:0xc000000000", false},
		{"DobbyVPN Block IPv6 windows-1-1; remove all", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsOwnedWindowsIPv6RuleName(test.name); got != test.want {
				t.Fatalf("IsOwnedWindowsIPv6RuleName(%q)=%t, want %t", test.name, got, test.want)
			}
		})
	}
}
