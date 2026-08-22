//go:build linux && !(android || ios)

package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseLinuxDefaultRouteSkipsTunDefault(t *testing.T) {
	gotGateway, gotIface, err := parseLinuxDefaultRoute("default dev dobby0 proto static\ndefault via 192.0.2.1 dev eth0 proto dhcp metric 100\n")
	if err != nil {
		t.Fatal(err)
	}
	if gotGateway != "192.0.2.1" || gotIface != "eth0" {
		t.Fatalf("physical default = %s/%s, want 192.0.2.1/eth0", gotGateway, gotIface)
	}
}

func TestDiscoverLinuxDefaultRoutePreservesRouteOutputOnParseFailure(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	linuxRunCommand = func(string) (string, error) {
		return "default dev dobby0 proto static", nil
	}
	if _, _, err := DiscoverLinuxDefaultRoute(); err == nil || !strings.Contains(err.Error(), "default dev dobby0 proto static") {
		t.Fatalf("error = %v, want complete route output", err)
	}
}

func TestDiscoverLinuxDefaultRoutePreservesCommandDiagnostics(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var command string
	linuxRunCommand = func(got string) (string, error) {
		command = got
		return "", fmt.Errorf("route unavailable")
	}
	if _, _, err := DiscoverLinuxDefaultRoute(); err == nil || !strings.Contains(err.Error(), "route unavailable") {
		t.Fatalf("error = %v, want underlying route diagnostic", err)
	}
	if command != "ip -4 route show table main" {
		t.Fatalf("command = %q", command)
	}
}

func TestReconcileLinuxSessionRoutesWithRuleRestoresAllOwnedEntries(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		return "", nil
	}
	if err := ReconcileLinuxSessionRoutesWithRule("198.51.100.9", "192.0.2.1", "eth0", 233, 23333); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip route replace 198.51.100.9/32 via 192.0.2.1 dev eth0",
		"ip route replace table 233 default via 192.0.2.1 dev eth0",
		"ip rule add fwmark 233 lookup 233 priority 23333",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestReconcileLinuxSessionRoutesWithRuleToleratesExistingRule(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	linuxRunCommand = func(command string) (string, error) {
		if strings.HasPrefix(command, "ip rule add") {
			return "RTNETLINK answers: File exists", fmt.Errorf("RTNETLINK answers: File exists")
		}
		return "", nil
	}
	if err := ReconcileLinuxSessionRoutesWithRule("198.51.100.9", "192.0.2.1", "eth0", 233, 23333); err != nil {
		t.Fatalf("existing exact rule should be harmless: %v", err)
	}
}
