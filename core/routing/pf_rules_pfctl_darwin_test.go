//go:build darwin && !ios

package routing

import (
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMacOSPFRulesParseWithPfctl checks the generated kill switch rules with
// the real parser: `pfctl -n` parses the anchor and loads nothing. pfctl needs
// root, so the test runs only where DOBBY_PFCTL_PARSE=1 and passwordless sudo
// are set up (the macOS CI runner), never on a developer Mac by accident.
func TestMacOSPFRulesParseWithPfctl(t *testing.T) {
	if os.Getenv("DOBBY_PFCTL_PARSE") != "1" {
		t.Skip("set DOBBY_PFCTL_PARSE=1 on a host with passwordless sudo to parse with pfctl")
	}
	servers := []netip.Addr{netip.MustParseAddr("192.168.1.1"), netip.MustParseAddr("fe80::1%en0")}
	destinations := []PFDestination{
		{"tcp", netip.MustParseAddr("203.0.113.10"), 443, false},
		{"udp", netip.MustParseAddr("203.0.113.10"), 53, false},
		{"tcp", netip.MustParseAddr("2001:db8::5"), 0, true},
	}
	large := make([]netip.Prefix, 0, 14000)
	for i := range 14000 { // every other /24, so nothing joins
		large = append(large, netip.MustParsePrefix(fmt.Sprintf("11.%d.%d.0/24", i/128, i%128*2)))
	}
	for name, excluded := range map[string][]netip.Prefix{
		"empty": nil,
		"small": {netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("2001:db8::/32")},
		"14000": large,
	} {
		t.Run(name, func(t *testing.T) {
			rules := MacOSPFRules("utun7", servers, excluded, destinations)
			path := filepath.Join(t.TempDir(), "dobbyvpn.pf")
			if err := os.WriteFile(path, []byte(rules), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.CommandContext(t.Context(), "sudo", "-n", "/sbin/pfctl", "-n", "-a", MacOSPFAnchor, "-f", path).CombinedOutput()
			if err != nil {
				t.Fatalf("pfctl rejected the %s rules (%d bytes, longest line %d bytes): %v\n%s", name, len(rules), longestLine(rules), err, output)
			}
		})
	}
}

func longestLine(text string) int {
	longest := 0
	for _, line := range strings.Split(text, "\n") {
		longest = max(longest, len(line))
	}
	return longest
}
