//go:build windows

package platform_engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"core/routing"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows/registry"
)

const (
	nrptCIName      = "dobbyvpn-nrpt-ci.test."
	nrptCIAnswer    = "192.0.2.77"
	nrptCIOwnerEnv  = "DOBBY_NRPT_OWNER_PROBE"
	nrptCIOwnedPath = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig\` + routing.OwnedNRPTRuleKey
)

// TestWindowsNRPTRuleOnRealDNSClient runs the product's NRPT code against the
// real registry and DNS Client. A local DNS responder plays the tunnel DNS
// server, so the test sees where the DNS Client sends a query while the rule
// exists and after it is removed. It changes the machine's DNS, so it runs only
// where DOBBY_NRPT_LIVE=1 is set (the Windows CI runner). It removes only rules
// it created itself, after checking they are still the ones it created.
func TestWindowsNRPTRuleOnRealDNSClient(t *testing.T) {
	if os.Getenv("DOBBY_NRPT_LIVE") != "1" {
		t.Skip("set DOBBY_NRPT_LIVE=1 on a disposable Windows host to change its NRPT")
	}
	if existing := registryValues(t, nrptCIOwnedPath); existing != nil {
		t.Fatalf("the host already has a key at the DobbyVPN NRPT key; not touching it: %v", existing)
	}
	foreignComment := fmt.Sprintf("DobbyVPN CI foreign rule %d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupCIRules(t, foreignComment) })

	queries := startDNSResponder(t)
	responder := []netip.Addr{netip.MustParseAddr("127.0.0.1")}

	foreignName := strings.TrimSpace(string(mustPowershell(t, `(Add-DnsClientNrptRule -PassThru -Namespace '.dobbyvpn-ci.invalid' -NameServers 192.0.2.53 -Comment '`+foreignComment+`').Name`)))
	if foreignName == "" {
		t.Fatal("Add-DnsClientNrptRule returned no rule name")
	}

	t.Run("connect sends queries to the tunnel server", func(t *testing.T) {
		installWindowsNRPT(responder)
		rule := ownedRule(nrptRules(t))
		if rule == nil || rule.Namespace != "." || rule.NameServers != "127.0.0.1" || rule.Comment != routing.OwnedNRPTComment {
			t.Fatalf("Get-DnsClientNrptRule after connect: %+v", nrptRules(t))
		}
		waitForPolicy(t, "127.0.0.1", true)
		before := queries.Load()
		started := time.Now()
		answer := resolveA(t)
		t.Logf("Resolve-DnsName %s took %s and returned %q", nrptCIName, time.Since(started), answer)
		if answer != nrptCIAnswer || queries.Load() == before {
			t.Fatalf("the DNS Client did not ask the tunnel server: answer=%q queries=%d", answer, queries.Load()-before)
		}
	})

	t.Run("stop removes the rule and DNS stops using it", func(t *testing.T) {
		if err := EngineStop(context.Background()); err != nil {
			t.Fatalf("EngineStop: %v", err)
		}
		if rule := ownedRule(nrptRules(t)); rule != nil {
			t.Fatalf("rule left after Stop: %+v", rule)
		}
		waitForPolicy(t, "127.0.0.1", false)
		before := queries.Load()
		if answer := resolveA(t); answer == nrptCIAnswer || queries.Load() != before {
			t.Fatalf("after Stop the DNS Client still asked the tunnel server: answer=%q", answer)
		}
		assertForeignKept(t, foreignName)
	})

	t.Run("startup recovery after a crash", func(t *testing.T) {
		installWindowsNRPT(responder)
		windowsNRPTInstalled = false // a new process knows nothing about it
		if err := RecoverStaleWindowsNRPTRule(0); err != nil {
			t.Fatalf("startup recovery: %v", err)
		}
		if registryValues(t, nrptCIOwnedPath) != nil {
			t.Fatal("startup recovery left the owned key")
		}
		waitForPolicy(t, "127.0.0.1", false)
	})

	t.Run("startup recovery after an interrupted write or removal", func(t *testing.T) {
		for name, values := range map[string]map[string]string{
			"empty key":        {},
			"comment only":     {"Comment": routing.OwnedNRPTComment},
			"no ConfigOptions": {"Comment": routing.OwnedNRPTComment, "GenericDNSServers": "127.0.0.1"},
		} {
			writeKey(t, nrptCIOwnedPath, values)
			if err := RecoverStaleWindowsNRPTRule(0); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if registryValues(t, nrptCIOwnedPath) != nil {
				t.Fatalf("%s: key left after recovery", name)
			}
		}
	})

	t.Run("another process cannot take ownership", func(t *testing.T) {
		probe := exec.Command(os.Args[0], "-test.run=^TestNRPTOwnerProbe$", "-test.count=1")
		probe.Env = append(os.Environ(), nrptCIOwnerEnv+"=1")
		output, err := probe.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "owner busy") {
			t.Fatalf("second process: err=%v output=%s", err, output)
		}
	})

	t.Run("a foreign rule at the owned key is never touched", func(t *testing.T) {
		writeKey(t, nrptCIOwnedPath, map[string]string{"Comment": foreignComment, "GenericDNSServers": "192.0.2.54"})
		if err := RecoverStaleWindowsNRPTRule(0); err != nil {
			t.Fatalf("startup recovery with a foreign key: %v", err)
		}
		if err := routing.InstallWindowsNRPT(responder); !errors.Is(err, routing.ErrForeignNRPTRule) {
			t.Fatalf("install over a foreign key: %v", err)
		}
		if values := registryValues(t, nrptCIOwnedPath); values["Comment"] != foreignComment || values["GenericDNSServers"] != "192.0.2.54" {
			t.Fatalf("foreign key changed: %v", values)
		}
		assertForeignKept(t, foreignName)
	})
}

// TestNRPTOwnerProbe runs in a second process started by the test above.
func TestNRPTOwnerProbe(t *testing.T) {
	if os.Getenv(nrptCIOwnerEnv) != "1" {
		t.Skip("helper process only")
	}
	if _, err := routing.AcquireWindowsNRPTOwnership(500 * time.Millisecond); err == nil {
		t.Fatal("a second process took the NRPT owner mutex")
	}
	if _, err := routing.RemoveWindowsNRPT(); err == nil {
		t.Fatal("a second process changed the NRPT rule")
	}
	fmt.Println("owner busy")
}

func startDNSResponder(t *testing.T) *atomic.Int64 {
	t.Helper()
	var queries atomic.Int64
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		queries.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(r)
		if len(r.Question) == 1 && strings.EqualFold(r.Question[0].Name, nrptCIName) && r.Question[0].Qtype == dns.TypeA {
			reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1},
				A: net.ParseIP(nrptCIAnswer)})
		}
		_ = w.WriteMsg(reply)
	})
	for _, network := range []string{"udp", "tcp"} {
		started := make(chan struct{})
		server := &dns.Server{Addr: "127.0.0.1:53", Net: network, Handler: handler, NotifyStartedFunc: func() { close(started) }}
		failed := make(chan error, 1)
		go func() { failed <- server.ListenAndServe() }()
		select {
		case <-started:
		case err := <-failed:
			t.Fatalf("start %s DNS responder on 127.0.0.1:53: %v", network, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s DNS responder did not start", network)
		}
		t.Cleanup(func() { _ = server.Shutdown() })
	}
	return &queries
}

func resolveA(t *testing.T) string {
	t.Helper()
	mustPowershell(t, `Clear-DnsClientCache`)
	output, _ := powershell(`(Resolve-DnsName -Name '` + strings.TrimSuffix(nrptCIName, ".") + `' -Type A -DnsOnly -QuickTimeout -ErrorAction SilentlyContinue | Where-Object { $_.Type -eq 'A' } | Select-Object -First 1).IPAddress`)
	return strings.TrimSpace(string(output))
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

func mustPowershell(t *testing.T, script string) []byte {
	t.Helper()
	output, err := powershell(script)
	if err != nil {
		t.Fatalf("%s: %v", script, err)
	}
	return output
}

func nrptRules(t *testing.T) []nrptRuleView {
	t.Helper()
	output := mustPowershell(t, `ConvertTo-Json -Compress -InputObject @(Get-DnsClientNrptRule | ForEach-Object {
		[pscustomobject]@{ Name = [string]$_.Name; Namespace = (@($_.Namespace) -join ',');
		NameServers = (@($_.NameServers) -join ','); Comment = [string]$_.Comment } })`)
	var rules []nrptRuleView
	if err := json.Unmarshal(output, &rules); err != nil {
		t.Fatalf("parse Get-DnsClientNrptRule %q: %v", output, err)
	}
	return rules
}

func ownedRule(rules []nrptRuleView) *nrptRuleView {
	for i := range rules {
		if strings.EqualFold(rules[i].Name, routing.OwnedNRPTRuleKey) {
			return &rules[i]
		}
	}
	return nil
}

// waitForPolicy waits until the DNS Client's effective table does (or does
// not) send the root namespace to server.
func waitForPolicy(t *testing.T, server string, present bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		output := mustPowershell(t, `ConvertTo-Json -Compress -InputObject @(Get-DnsClientNrptPolicy | ForEach-Object {
			[pscustomobject]@{ Namespace = (@($_.Namespace) -join ','); NameServers = (@($_.NameServers) -join ',') } })`)
		var policy []nrptRuleView
		if err := json.Unmarshal(output, &policy); err != nil {
			t.Fatalf("parse Get-DnsClientNrptPolicy %q: %v", output, err)
		}
		found := false
		for _, entry := range policy {
			found = found || (entry.Namespace == "." && strings.Contains(entry.NameServers, server))
		}
		if found == present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("effective NRPT policy root_rule=%t, want %t: %s", found, present, output)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertForeignKept(t *testing.T, name string) {
	t.Helper()
	for _, rule := range nrptRules(t) {
		if strings.EqualFold(rule.Name, name) {
			return
		}
	}
	t.Fatalf("the foreign rule %s was removed", name)
}

// registryValues returns the string and DWORD values of key, or nil if absent.
func registryValues(t *testing.T, path string) map[string]string {
	t.Helper()
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, name := range names {
		if text, _, err := k.GetStringValue(name); err == nil {
			values[name] = text
		} else if number, _, err := k.GetIntegerValue(name); err == nil {
			values[name] = fmt.Sprint(number)
		} else {
			values[name] = "?"
		}
	}
	return values
}

func writeKey(t *testing.T, path string, values map[string]string) {
	t.Helper()
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	for name, value := range values {
		if err := k.SetStringValue(name, value); err != nil {
			t.Fatal(err)
		}
	}
}

// cleanupCIRules removes only what this test created: the owned key if it is
// still ours or carries this run's foreign comment, and the foreign rule with
// this run's comment.
func cleanupCIRules(t *testing.T, foreignComment string) {
	if values := registryValues(t, nrptCIOwnedPath); values != nil {
		if values["Comment"] == foreignComment {
			_ = registry.DeleteKey(registry.LOCAL_MACHINE, nrptCIOwnedPath)
		} else if _, err := routing.RemoveWindowsNRPT(); err != nil {
			t.Errorf("cleanup of the owned NRPT key: %v", err)
		}
	}
	windowsNRPTInstalled = false
	if _, err := powershell(`Get-DnsClientNrptRule | Where-Object { $_.Comment -eq '` + foreignComment + `' } | Remove-DnsClientNrptRule -Force`); err != nil {
		t.Errorf("cleanup of the foreign CI rule: %v", err)
	}
}
