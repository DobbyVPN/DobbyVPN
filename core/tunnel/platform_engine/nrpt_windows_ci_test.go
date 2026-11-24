//go:build windows

package platform_engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"core/routing"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows/registry"
)

const (
	nrptCITunnelName     = "dobbyvpn-nrpt-ci.test."
	nrptCITunnelAnswer   = "192.0.2.77"
	nrptCIOrdinaryZone   = ".dobbyvpn-ci.invalid"
	nrptCIOrdinaryName   = "probe.dobbyvpn-ci.invalid."
	nrptCIOrdinaryAnswer = "192.0.2.88"
	nrptCIHelperEnv      = "DOBBY_NRPT_HELPER"
	nrptCIOwnedPath      = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig\` + routing.OwnedNRPTRuleKey
	nrptCIResolveBound   = 2 * time.Second
)

// nrptCIState records what this run created, so the workflow's fallback step
// removes only that. It is written to DOBBY_NRPT_CI_STATE when set.
type nrptCIState struct {
	Marker         string
	Refused        bool   // the test did not start because the key existed
	ForeignRule    string // Name of the rule added with Add-DnsClientNrptRule
	ForeignComment string
	OwnedKey       bool // this run created the key at the DobbyVPN GUID
}

func (s *nrptCIState) save(t *testing.T) {
	path := os.Getenv("DOBBY_NRPT_CI_STATE")
	if path == "" {
		return
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Errorf("write CI state: %v", err)
	}
}

// TestWindowsNRPTRuleOnRealDNSClient runs the product's NRPT code against the
// real registry, DNS Client and owner mutex. Two local DNS responders play the
// tunnel server (127.0.0.1) and an ordinary server reached through a separate,
// more specific rule (127.0.0.2). It changes the machine's DNS, so it runs only
// where DOBBY_NRPT_LIVE=1 is set (the Windows CI runner). It removes only what
// it created, after checking it is still what it created.
func TestWindowsNRPTRuleOnRealDNSClient(t *testing.T) {
	if os.Getenv("DOBBY_NRPT_LIVE") != "1" {
		t.Skip("set DOBBY_NRPT_LIVE=1 on a disposable Windows host to change its NRPT")
	}
	marker := os.Getenv("DOBBY_NRPT_CI_MARKER")
	if marker == "" {
		marker = strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	state := &nrptCIState{Marker: marker, ForeignComment: "DobbyVPN CI foreign rule " + marker}
	if existing := registryValues(t, nrptCIOwnedPath); existing != nil {
		state.Refused = true
		state.save(t)
		t.Fatalf("the host already has a key at the DobbyVPN NRPT key; not touching it: %v", existing)
	}
	state.save(t)
	t.Cleanup(func() { cleanupCIRules(t, state) })

	tunnelQueries := startDNSResponder(t, "127.0.0.1", nrptCITunnelName, nrptCITunnelAnswer)
	ordinaryQueries := startDNSResponder(t, "127.0.0.2", nrptCIOrdinaryName, nrptCIOrdinaryAnswer)

	state.ForeignRule = strings.TrimSpace(string(mustPowershell(t, `(Add-DnsClientNrptRule -PassThru -Namespace '`+
		nrptCIOrdinaryZone+`' -NameServers 127.0.0.2 -Comment '`+state.ForeignComment+`').Name`)))
	state.save(t)
	if state.ForeignRule == "" {
		t.Fatal("Add-DnsClientNrptRule returned no rule name")
	}
	waitForPolicy(t, nrptCIOrdinaryZone, "127.0.0.2", true)
	expectAnswer(t, nrptCIOrdinaryName, nrptCIOrdinaryAnswer, ordinaryQueries)

	// These run before this process takes the owner mutex itself.
	t.Run("owner mutex across processes", func(t *testing.T) {
		holder := startHelper(t, "hold", "held")
		if out := runHelper(t, "probe"); out != "busy" {
			t.Fatalf("second process while the owner lives: %q", out)
		}
		if out := runHelper(t, "recover:300"); !strings.HasPrefix(out, "skipped") {
			t.Fatalf("release-protection style recovery with a short wait: %q", out)
		}
		waiter := startHelper(t, "recover:10000", "")
		time.Sleep(500 * time.Millisecond)
		holder.kill(t)
		if out := waiter.next(t, 10*time.Second); out != "recovered" {
			t.Fatalf("recovery waiting for a killed owner: %q", out)
		}
		waiter.wait(t)
	})

	t.Run("rule left by a killed process is removed at start", func(t *testing.T) {
		state.OwnedKey = true
		state.save(t)
		owner := startHelper(t, "install", "installed")
		if ownedRule(nrptRules(t)) == nil {
			t.Fatal("helper did not install the rule")
		}
		owner.kill(t)
		if err := RecoverStaleWindowsNRPTRule(5 * time.Second); err != nil {
			t.Fatalf("startup recovery: %v", err)
		}
		if registryValues(t, nrptCIOwnedPath) != nil {
			t.Fatal("startup recovery left the owned key")
		}
		waitForPolicy(t, ".", "127.0.0.1", false)
	})

	t.Run("connect sends other names to the tunnel server", func(t *testing.T) {
		installWindowsNRPT([]netip.Addr{netip.MustParseAddr("127.0.0.1")})
		rule := ownedRule(nrptRules(t))
		if rule == nil || rule.Namespace != "." || rule.NameServers != "127.0.0.1" || rule.Comment != routing.OwnedNRPTComment {
			t.Fatalf("Get-DnsClientNrptRule after connect: %+v", nrptRules(t))
		}
		waitForPolicy(t, ".", "127.0.0.1", true)
		expectAnswer(t, nrptCITunnelName, nrptCITunnelAnswer, tunnelQueries)
		// A more specific rule of another owner still wins.
		expectAnswer(t, nrptCIOrdinaryName, nrptCIOrdinaryAnswer, ordinaryQueries)
	})

	t.Run("stop removes the rule and ordinary DNS answers again", func(t *testing.T) {
		if err := EngineStop(context.Background()); err != nil {
			t.Fatalf("EngineStop: %v", err)
		}
		if rule := ownedRule(nrptRules(t)); rule != nil {
			t.Fatalf("rule left after Stop: %+v", rule)
		}
		waitForPolicy(t, ".", "127.0.0.1", false)
		before := tunnelQueries.Load()
		expectAnswer(t, nrptCIOrdinaryName, nrptCIOrdinaryAnswer, ordinaryQueries)
		if _, _, err := resolve(t, "github.com"); err != nil {
			t.Fatalf("ordinary DNS after Stop: %v", err)
		}
		_, _, _ = resolve(t, nrptCITunnelName) // not answered by the runner's DNS
		if tunnelQueries.Load() != before {
			t.Fatal("after Stop the DNS Client still asked the tunnel server")
		}
	})

	t.Run("startup recovery after an interrupted write or removal", func(t *testing.T) {
		for name, values := range map[string]map[string]string{
			"empty key":        {},
			"comment only":     {"Comment": routing.OwnedNRPTComment},
			"no ConfigOptions": {"Comment": routing.OwnedNRPTComment, "GenericDNSServers": "127.0.0.1"},
			"extra values":     {"Comment": routing.OwnedNRPTComment, "Extra": "x"},
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

	t.Run("a foreign key at the owned name is never touched", func(t *testing.T) {
		writeKey(t, nrptCIOwnedPath, map[string]string{"Comment": state.ForeignComment, "GenericDNSServers": "192.0.2.54"})
		if err := RecoverStaleWindowsNRPTRule(0); err != nil {
			t.Fatalf("startup recovery with a foreign key: %v", err)
		}
		if err := routing.InstallWindowsNRPT([]netip.Addr{netip.MustParseAddr("127.0.0.1")}); !errors.Is(err, routing.ErrForeignNRPTRule) {
			t.Fatalf("install over a foreign key: %v", err)
		}
		if values := registryValues(t, nrptCIOwnedPath); values["Comment"] != state.ForeignComment || values["GenericDNSServers"] != "192.0.2.54" {
			t.Fatalf("foreign key changed: %v", values)
		}
	})
}

// TestNRPTOwnerHelper is a second process started by the test above.
func TestNRPTOwnerHelper(t *testing.T) {
	mode := os.Getenv(nrptCIHelperEnv)
	if mode == "" {
		t.Skip("helper process only")
	}
	switch {
	case mode == "hold":
		if _, err := routing.AcquireWindowsNRPTOwnership(0); err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Println("held")
		for {
			time.Sleep(time.Hour)
		}
	case mode == "probe":
		if _, err := routing.AcquireWindowsNRPTOwnership(300 * time.Millisecond); err == nil {
			fmt.Println("acquired")
		} else if _, err := routing.RemoveWindowsNRPT(); err == nil {
			fmt.Println("changed the rule without ownership")
		} else {
			fmt.Println("busy")
		}
	case strings.HasPrefix(mode, "recover:"):
		ms, _ := strconv.Atoi(strings.TrimPrefix(mode, "recover:"))
		if err := RecoverStaleWindowsNRPTRule(time.Duration(ms) * time.Millisecond); err != nil {
			fmt.Println("skipped:", err)
		} else {
			fmt.Println("recovered")
		}
	case mode == "install":
		installWindowsNRPT([]netip.Addr{netip.MustParseAddr("127.0.0.1")})
		if !windowsNRPTInstalled {
			fmt.Println("not installed")
			return
		}
		fmt.Println("installed")
		for {
			time.Sleep(time.Hour)
		}
	}
}

type nrptHelper struct {
	cmd   *exec.Cmd
	lines chan string
}

func startHelper(t *testing.T, mode, ready string) *nrptHelper {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestNRPTOwnerHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), nrptCIHelperEnv+"="+mode)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &nrptHelper{cmd: cmd, lines: make(chan string, 16)}
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			h.lines <- strings.TrimSpace(scanner.Text())
		}
		close(h.lines)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if ready != "" {
		if out := h.next(t, 10*time.Second); out != ready {
			t.Fatalf("helper %s: %q, want %q", mode, out, ready)
		}
	}
	return h
}

// next returns the helper's next line of its own output, skipping the test
// framework's lines.
func (h *nrptHelper) next(t *testing.T, wait time.Duration) string {
	t.Helper()
	deadline := time.After(wait)
	for {
		select {
		case line, ok := <-h.lines:
			if !ok {
				return "helper exited"
			}
			if line == "" || strings.HasPrefix(line, "PASS") || strings.HasPrefix(line, "ok") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "===") {
				continue
			}
			return line
		case <-deadline:
			t.Fatal("helper did not answer")
			return ""
		}
	}
}

func (h *nrptHelper) kill(t *testing.T) {
	t.Helper()
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = h.cmd.Wait()
}

func (h *nrptHelper) wait(t *testing.T) {
	t.Helper()
	for range h.lines {
	}
	_ = h.cmd.Wait()
}

func runHelper(t *testing.T, mode string) string {
	t.Helper()
	h := startHelper(t, mode, "")
	out := h.next(t, 10*time.Second)
	h.wait(t)
	return out
}

func startDNSResponder(t *testing.T, address, name, answer string) *atomic.Int64 {
	t.Helper()
	var queries atomic.Int64
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		queries.Add(1)
		reply := new(dns.Msg)
		reply.SetReply(r)
		if len(r.Question) == 1 && strings.EqualFold(r.Question[0].Name, name) && r.Question[0].Qtype == dns.TypeA {
			reply.Answer = append(reply.Answer, &dns.A{Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 1},
				A: net.ParseIP(answer)})
		}
		_ = w.WriteMsg(reply)
	})
	for _, network := range []string{"udp", "tcp"} {
		started := make(chan struct{})
		server := &dns.Server{Addr: address + ":53", Net: network, Handler: handler, NotifyStartedFunc: func() { close(started) }}
		failed := make(chan error, 1)
		go func() { failed <- server.ListenAndServe() }()
		select {
		case <-started:
		case err := <-failed:
			t.Fatalf("start %s DNS responder on %s:53: %v", network, address, err)
		case <-time.After(5 * time.Second):
			t.Fatalf("%s DNS responder on %s did not start", network, address)
		}
		t.Cleanup(func() { _ = server.Shutdown() })
	}
	return &queries
}

// resolve asks the DNS Client for an A record of name, bypassing its cache. It
// returns the address and the time Resolve-DnsName itself took, measured
// inside PowerShell so that starting PowerShell is not counted.
func resolve(t *testing.T, name string) (string, time.Duration, error) {
	t.Helper()
	output, err := powershell(`Clear-DnsClientCache; $r = $null
		$ms = (Measure-Command { $r = Resolve-DnsName -Name '` + strings.TrimSuffix(name, ".") + `' -Type A -DnsOnly -QuickTimeout |
			Where-Object { $_.Type -eq 'A' } | Select-Object -First 1 }).TotalMilliseconds
		if (-not $r) { throw 'no A record' }; "$($r.IPAddress) $([int]$ms)"`)
	fields := strings.Fields(strings.TrimSpace(string(output)))
	if err != nil || len(fields) != 2 {
		return "", 0, errors.Join(err, fmt.Errorf("unexpected Resolve-DnsName output %q", output))
	}
	ms, convErr := strconv.Atoi(fields[1])
	return fields[0], time.Duration(ms) * time.Millisecond, convErr
}

func expectAnswer(t *testing.T, name, want string, queries *atomic.Int64) {
	t.Helper()
	before := queries.Load()
	started := time.Now()
	answer, lookup, err := resolve(t, name)
	t.Logf("Resolve-DnsName %s: %q lookup=%s with_powershell=%s", name, answer, lookup, time.Since(started))
	if err != nil || answer != want || queries.Load() == before {
		t.Fatalf("Resolve-DnsName %s: answer=%q err=%v responder_queries=%d, want %s", name, answer, err, queries.Load()-before, want)
	}
	if lookup > nrptCIResolveBound {
		t.Fatalf("Resolve-DnsName %s took %s, more than %s", name, lookup, nrptCIResolveBound)
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
// not) send namespace to server.
func waitForPolicy(t *testing.T, namespace, server string, present bool) {
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
			found = found || (entry.Namespace == namespace && strings.Contains(entry.NameServers, server))
		}
		if found == present {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("effective NRPT policy %s->%s=%t, want %t: %s", namespace, server, found, present, output)
		}
		time.Sleep(250 * time.Millisecond)
	}
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

// cleanupCIRules removes only what this run created: the owned key if this
// run created it and it still carries the DobbyVPN comment or this run's
// foreign comment, and the foreign rule by its recorded name and comment.
func cleanupCIRules(t *testing.T, state *nrptCIState) {
	if state.OwnedKey {
		switch values := registryValues(t, nrptCIOwnedPath); {
		case values == nil:
		case values["Comment"] == state.ForeignComment:
			_ = registry.DeleteKey(registry.LOCAL_MACHINE, nrptCIOwnedPath)
		default:
			if _, err := routing.RemoveWindowsNRPT(); err != nil {
				t.Errorf("cleanup of the owned NRPT key: %v", err)
			}
		}
	}
	windowsNRPTInstalled = false
	if state.ForeignRule != "" {
		if _, err := powershell(`Get-DnsClientNrptRule -Name '` + state.ForeignRule + `' -ErrorAction SilentlyContinue |
			Where-Object { $_.Comment -eq '` + state.ForeignComment + `' } | Remove-DnsClientNrptRule -Force`); err != nil {
			t.Errorf("cleanup of the foreign CI rule: %v", err)
		}
	}
	state.save(t)
}
