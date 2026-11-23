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
//
// With DOBBY_PFCTL_LOAD=1 as well, the rules are also loaded into the kernel,
// into the top-level anchor pfCILoadAnchor. The main ruleset never refers to
// that anchor, so its rules are never evaluated and the runner's traffic is
// not affected; the test checks the kernel table size and flushes the anchor.
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
			output, err := pfctl(t, "-n", "-a", MacOSPFAnchor, "-f", path)
			if err != nil {
				t.Fatalf("pfctl rejected the %s rules (%d bytes, longest line %d bytes): %v\n%s", name, len(rules), longestLine(rules), err, output)
			}
			if os.Getenv("DOBBY_PFCTL_LOAD") != "1" {
				return
			}
			t.Cleanup(func() {
				for _, kind := range []string{"rules", "Tables"} {
					if flushed, err := pfctl(t, "-a", pfCILoadAnchor, "-F", kind); err != nil {
						t.Errorf("flush %s of %s: %v\n%s", kind, pfCILoadAnchor, err, flushed)
					}
				}
			})
			if loaded, err := pfctl(t, "-a", pfCILoadAnchor, "-f", path); err != nil {
				t.Fatalf("pf did not load the %s rules: %v\n%s", name, err, loaded)
			}
			if len(excluded) == 0 {
				return
			}
			output, err = pfctl(t, "-a", pfCILoadAnchor, "-t", "dobbyvpn_excluded", "-T", "show")
			if err != nil {
				t.Fatalf("show the loaded %s table: %v\n%s", name, err, output)
			}
			if entries := len(strings.Fields(string(output))); entries != len(excluded) {
				t.Fatalf("the kernel table holds %d entries, want %d", entries, len(excluded))
			}
		})
	}
}

// pfCILoadAnchor is a top-level anchor the main ruleset never refers to.
const pfCILoadAnchor = "dobbyvpn-ci"

func pfctl(t *testing.T, args ...string) ([]byte, error) {
	t.Helper()
	return exec.CommandContext(t.Context(), "sudo", append([]string{"-n", "/sbin/pfctl"}, args...)...).CombinedOutput()
}

func longestLine(text string) int {
	longest := 0
	for _, line := range strings.Split(text, "\n") {
		longest = max(longest, len(line))
	}
	return longest
}
