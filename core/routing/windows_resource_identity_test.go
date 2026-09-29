package routing

import "testing"

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
