//go:build windows

package platform_engine

import (
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"core/routing"

	"golang.org/x/sys/windows/registry"
)

// TestWindowsNRPTRuleOnRealDNSClient adds and removes the owned NRPT rule in
// the real registry and checks it with Get-DnsClientNrptRule (stored rules) and
// Get-DnsClientNrptPolicy (the table the DNS Client uses). It changes the
// machine's DNS, so it runs only where DOBBY_NRPT_LIVE=1 is set (the Windows
// CI runner), never on a developer machine by accident. Every rule it adds is
// removed in cleanup, also when the test fails.
func TestWindowsNRPTRuleOnRealDNSClient(t *testing.T) {
	if os.Getenv("DOBBY_NRPT_LIVE") != "1" {
		t.Skip("set DOBBY_NRPT_LIVE=1 on a disposable Windows host to change its NRPT")
	}
	const foreignNamespace = ".dobbyvpn-ci.invalid"
	servers := []netip.Addr{netip.MustParseAddr("9.9.9.9"), netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("2620:fe::fe"), netip.MustParseAddr("2606:4700:4700::1111")}
	ownedKey := `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig\` + routing.OwnedNRPTRuleKey
	t.Cleanup(func() {
		_ = registry.DeleteKey(registry.LOCAL_MACHINE, ownedKey)
		_, _ = powershell(`Get-DnsClientNrptRule | Where-Object { $_.Namespace -contains '` + foreignNamespace + `' } | Remove-DnsClientNrptRule -Force`)
		windowsNRPTInstalled = false
	})
	if rules := nrptRules(t); ownedRule(rules) != nil {
		t.Fatalf("the runner already has the owned rule: %+v", rules)
	}
	if _, err := powershell(`Add-DnsClientNrptRule -Namespace '` + foreignNamespace + `' -NameServers 192.0.2.53 -Comment 'foreign CI rule'`); err != nil {
		t.Fatalf("add foreign rule: %v", err)
	}
	before := len(nrptRules(t))

	// Connect.
	installWindowsNRPT(servers)
	rule := ownedRule(nrptRules(t))
	if rule == nil || rule.Namespace != "." || rule.NameServers != "9.9.9.9,1.1.1.1,2620:fe::fe,2606:4700:4700::1111" ||
		rule.Comment != routing.OwnedNRPTComment || !strings.EqualFold(rule.Name, routing.OwnedNRPTRuleKey) {
		t.Fatalf("Get-DnsClientNrptRule after install: %+v", nrptRules(t))
	}
	waitForPolicy(t, true)

	// Disconnect.
	if err := removeWindowsNRPT(); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if rules := nrptRules(t); ownedRule(rules) != nil || len(rules) != before {
		t.Fatalf("Get-DnsClientNrptRule after removal: %+v", rules)
	}
	waitForPolicy(t, false)
	assertForeignKept(t, foreignNamespace)

	// Crash: the rule stays in the registry; the next service start removes it.
	installWindowsNRPT(servers)
	windowsNRPTInstalled = false // a new process knows nothing about it
	if err := RecoverStaleWindowsNRPTRule(); err != nil {
		t.Fatalf("startup recovery: %v", err)
	}
	if ownedRule(nrptRules(t)) != nil {
		t.Fatal("startup recovery left the owned rule")
	}
	waitForPolicy(t, false)
	assertForeignKept(t, foreignNamespace)

	// A rule at our key that we did not write is never touched.
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, ownedKey, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	setErr := errors.Join(key.SetDWordValue("Version", 2), key.SetStringsValue("Name", []string{".other.invalid"}),
		key.SetStringValue("GenericDNSServers", "192.0.2.54"), key.SetDWordValue("ConfigOptions", 8),
		key.SetStringValue("IPSECCARestriction", ""), key.SetStringValue("Comment", "someone else"))
	key.Close()
	if setErr != nil {
		t.Fatal(setErr)
	}
	if err := RecoverStaleWindowsNRPTRule(); err != nil {
		t.Fatalf("startup recovery with a foreign rule at the owned key: %v", err)
	}
	if err := routing.InstallWindowsNRPT(servers); !errors.Is(err, routing.ErrForeignNRPTRule) {
		t.Fatalf("install over a foreign rule: %v", err)
	}
	if _, err := registry.OpenKey(registry.LOCAL_MACHINE, ownedKey, registry.QUERY_VALUE); err != nil {
		t.Fatalf("foreign rule at the owned key was removed: %v", err)
	}
}

type nrptRuleView struct {
	Name, Namespace, NameServers, Comment string
}

func powershell(script string) ([]byte, error) {
	output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$ErrorActionPreference='Stop'; "+script).CombinedOutput()
	if err != nil {
		return output, errors.Join(err, errors.New(string(output)))
	}
	return output, nil
}

func nrptRules(t *testing.T) []nrptRuleView {
	t.Helper()
	output, err := powershell(`ConvertTo-Json -Compress -InputObject @(Get-DnsClientNrptRule | ForEach-Object {
		[pscustomobject]@{ Name = [string]$_.Name; Namespace = (@($_.Namespace) -join ',');
		NameServers = (@($_.NameServers) -join ','); Comment = [string]$_.Comment } })`)
	if err != nil {
		t.Fatalf("Get-DnsClientNrptRule: %v", err)
	}
	var rules []nrptRuleView
	if err := json.Unmarshal(output, &rules); err != nil {
		t.Fatalf("parse Get-DnsClientNrptRule %q: %v", output, err)
	}
	return rules
}

func ownedRule(rules []nrptRuleView) *nrptRuleView {
	for i := range rules {
		if rules[i].Comment == routing.OwnedNRPTComment || strings.EqualFold(rules[i].Name, routing.OwnedNRPTRuleKey) {
			return &rules[i]
		}
	}
	return nil
}

// waitForPolicy waits until the DNS Client's effective table does (or does
// not) send the root namespace to the tunnel servers.
func waitForPolicy(t *testing.T, present bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		output, err := powershell(`ConvertTo-Json -Compress -InputObject @(Get-DnsClientNrptPolicy | ForEach-Object {
			[pscustomobject]@{ Namespace = (@($_.Namespace) -join ','); NameServers = (@($_.NameServers) -join ',') } })`)
		if err != nil {
			t.Fatalf("Get-DnsClientNrptPolicy: %v", err)
		}
		var policy []nrptRuleView
		if err := json.Unmarshal(output, &policy); err != nil {
			t.Fatalf("parse Get-DnsClientNrptPolicy %q: %v", output, err)
		}
		found := false
		for _, entry := range policy {
			found = found || (entry.Namespace == "." && strings.Contains(entry.NameServers, "9.9.9.9"))
		}
		if found == present {
			t.Logf("effective NRPT policy root_rule=%t: %s", found, output)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("effective NRPT policy root_rule=%t, want %t: %s", found, present, output)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertForeignKept(t *testing.T, namespace string) {
	t.Helper()
	for _, rule := range nrptRules(t) {
		if rule.Namespace == namespace {
			return
		}
	}
	t.Fatalf("the foreign rule %s was removed", namespace)
}
