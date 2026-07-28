//go:build darwin && !(android || ios)

package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	macOSBaselineDefault = "route to: default\ninterface: en0\ngateway: 192.0.2.1\n"
	macOSTunnelDefault   = "route to: default\ninterface: utun8\ngateway: link#17\n"
)

func TestMacOSTunnelDefaultRestoresCapturedBaseline(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	getCalls := 0
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if command == "route -n get default" {
			getCalls++
			if getCalls == 1 {
				return macOSBaselineDefault, nil
			}
			return macOSTunnelDefault, nil
		}
		return "", nil
	}

	plan := NewPlan("generation-31")
	if _, err := plan.AcquireMacOSTunnelDefault("utun8"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n get default",
		"route -n change default -interface utun8",
		"route -n get default",
		"route -n change default 192.0.2.1",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestMacOSRestoreScopedDefaultKeepsItsScope(t *testing.T) {
	command := macOSRestoreDefaultCommand(macOSDefaultRoute{
		gateway: "192.0.2.1",
		iface:   "en0",
		flags:   "<UP,GATEWAY,IFSCOPE>",
	})
	if want := "route -n change default 192.0.2.1 -ifscope en0"; command != want {
		t.Fatalf("restore command = %q, want %q", command, want)
	}
}

func TestMacOSTunnelDefaultDoesNotRestoreBaselineAfterOwnershipChanges(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	getCalls := 0
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if command == "route -n get default" {
			getCalls++
			if getCalls == 1 {
				return macOSBaselineDefault, nil
			}
			return "route to: default\ninterface: en7\ngateway: 198.51.100.1\n", nil
		}
		return "", nil
	}

	plan := NewPlan("generation-32")
	if _, err := plan.AcquireMacOSTunnelDefault("utun8"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err == nil {
		t.Fatal("Close succeeded after another actor changed the default route")
	}
	for _, command := range commands {
		if strings.Contains(command, "change default 192.0.2.1") {
			t.Fatalf("restored a baseline after ownership changed: %v", commands)
		}
	}
}

func TestMacOSIPv6LeasesPreserveExistingSinkRoutes(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "route -n add -inet6") {
			return "route: writing to routing socket: File exists", fmt.Errorf("File exists")
		}
		return "", nil
	}

	plan := NewPlan("generation-33")
	if err := plan.AcquireMacOSIPv6Block("utun8"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if strings.Contains(command, "route -n delete -inet6") {
			t.Fatalf("deleted a sink route that predated the session: %v", commands)
		}
	}
}

func TestMacOSRoutingFailureRollsBackOnlyResourcesAlreadyAcquired(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "8000::/1") && strings.Contains(command, "route -n add") {
			return "", fmt.Errorf("permission denied")
		}
		return "", nil
	}

	plan := NewPlan("generation-34")
	if err := plan.AcquireMacOSIPv6Block("utun8"); err == nil {
		t.Fatal("AcquireMacOSIPv6Block succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n add -inet6 -net ::/1 -interface utun8",
		"route -n add -inet6 -net 8000::/1 -interface utun8",
		"route -n delete -inet6 -net ::/1 -interface utun8",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestMacOSProxyLeaseDoesNotDeletePreexistingRoute(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "route -n add -host") {
			return "File exists", fmt.Errorf("File exists")
		}
		return "", nil
	}

	plan := NewPlan("generation-35")
	if _, err := plan.AcquireMacOSProxyRoute("198.51.100.8", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 || !strings.Contains(commands[0], "route -n add -host") {
		t.Fatalf("commands = %v; a pre-existing proxy route must not be deleted", commands)
	}
}
