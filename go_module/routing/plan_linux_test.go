//go:build linux && !(android || ios)

package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestLinuxProxyRouteLeasePreservesExistingRoute(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, " route add ") {
			return "", fmt.Errorf("RTNETLINK answers: File exists")
		}
		return "", nil
	}

	plan := NewPlan("generation-19")
	lease, err := plan.AcquireLinuxProxyRoute("198.51.100.8", "192.0.2.1", "eth0")
	if err != nil {
		t.Fatal(err)
	}
	if lease == nil {
		t.Fatal("expected lease")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "route add") {
		t.Fatalf("commands = %v; existing route must not be deleted", commands)
	}
}

func TestLinuxTunnelDefaultRestoresCapturedBaseline(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if command == "ip -o -4 route show default" {
			return "default via 192.0.2.1 dev eth0 proto dhcp metric 100\n", nil
		}
		return "", nil
	}

	plan := NewPlan("generation-20")
	if _, err := plan.AcquireLinuxTunnelDefault("dobby0"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip -o -4 route show default",
		"ip route replace default dev dobby0",
		"ip route del default dev dobby0",
		"ip route replace default via 192.0.2.1 dev eth0 proto dhcp metric 100",
	}
	if strings.Join(commands, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestLinuxMarkedRoutingFailureRollsBackOnlyRouteCreatedByPlan(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "ip rule add") {
			return "", fmt.Errorf("permission denied")
		}
		return "", nil
	}

	plan := NewPlan("generation-23")
	if err := plan.AcquireLinuxMarkedRouting(233, 23333, "eth0", "192.0.2.1"); err == nil {
		t.Fatal("AcquireLinuxMarkedRouting succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip route add table 233 default via 192.0.2.1 dev eth0",
		"ip rule add fwmark 233 lookup 233 priority 23333",
		"ip route del table 233 default via 192.0.2.1 dev eth0",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestLinuxTunnelDefaultDoesNotRestoreBaselineWhenOwnedRouteIsGone(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		switch command {
		case "ip -o -4 route show default":
			return "default via 192.0.2.1 dev eth0 proto dhcp metric 100\n", nil
		case "ip route del default dev dobby0":
			return "", fmt.Errorf("No such process")
		default:
			return "", nil
		}
	}

	plan := NewPlan("generation-24")
	if _, err := plan.AcquireLinuxTunnelDefault("dobby0"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err == nil {
		t.Fatal("Close succeeded after session-owned route was already gone")
	}
	for _, command := range commands {
		if strings.Contains(command, "ip route replace default via 192.0.2.1") {
			t.Fatalf("restored baseline after failing to remove owned route: %v", commands)
		}
	}
}

func TestLinuxIPv6BlockPreservesExistingRoutes(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, " route add blackhole ") {
			return "", fmt.Errorf("RTNETLINK answers: File exists")
		}
		return "", nil
	}

	plan := NewPlan("generation-25")
	if err := plan.AcquireLinuxIPv6Block(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.Contains(command, " route del blackhole ") {
			t.Fatalf("deleted a pre-existing IPv6 block route: %v", commands)
		}
	}
}
