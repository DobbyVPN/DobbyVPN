//go:build linux && !(android || ios)

package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRecoverLinuxOwnedRoutesDoesNothingWithoutTaggedRoutes(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		return "", nil
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip -o -4 route show table main",
		"ip -o -4 route show table 233",
		"ip -o -6 route show table main",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRecoverLinuxOwnedRoutesAcceptsAbsentMarkedTable(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if command == "ip -o -4 route show table 233" {
			return "Error: ipv4: FIB table does not exist.\nDump terminated\n", fmt.Errorf("exit status 2")
		}
		return "", nil
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("commands = %#v", commands)
	}
}

func TestRecoverLinuxOwnedRoutesDeletesOnlyRecognizedTaggedResources(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		switch command {
		case "ip -o -4 route show table main":
			return "198.51.100.8/32 via 192.0.2.1 dev eth0 proto 233 metric 233\n" +
				"default dev dobby233 proto 233\n" +
				"203.0.113.0/24 via 192.0.2.1 dev eth0 proto 233 metric 233\n", nil
		case "ip -o -4 route show table 233":
			return "default via 192.0.2.1 dev eth0 proto 233\n", nil
		case "ip -o -6 route show table main":
			return "blackhole ::/1 proto 233 metric 1 pref medium\n" +
				"blackhole 8000::/1 proto 233 metric 1 pref medium\n" +
				"blackhole 2001:db8::/32 proto 233 metric 1 pref medium\n", nil
		default:
			return "", nil
		}
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip -o -4 route show table main",
		"ip -o -4 route show table 233",
		"ip -o -6 route show table main",
		"ip rule del fwmark 233 lookup 233 priority 23333",
		"ip -4 route del table main 198.51.100.8/32 via 192.0.2.1 dev eth0 proto 233 metric 233",
		"ip -4 route del table main default dev dobby233 proto 233",
		"ip -4 route del table 233 default via 192.0.2.1 dev eth0 proto 233",
		"ip -6 route del table main blackhole ::/1 proto 233 metric 1",
		"ip -6 route del table main blackhole 8000::/1 proto 233 metric 1",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
}

func TestRecoverLinuxOwnedRoutesPreservesUntaggedAndUnrecognizedRoutes(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		switch command {
		case "ip -o -4 route show table main":
			return "198.51.100.8/32 via 192.0.2.1 dev eth0 proto static metric 233\n" +
				"203.0.113.0/24 via 192.0.2.1 dev eth0 proto 233 metric 233\n", nil
		case "ip -o -4 route show table 233":
			return "default via 192.0.2.1 dev eth0 proto static\n", nil
		case "ip -o -6 route show table main":
			return "blackhole 2001:db8::/32 proto 233 metric 1 pref medium\n", nil
		default:
			return "", nil
		}
	}

	if err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233"); err != nil {
		t.Fatal(err)
	}
	if len(commands) != 3 {
		t.Fatalf("unexpected cleanup commands: %#v", commands)
	}
}

func TestRecoverLinuxOwnedRoutesReportsCleanupFailureAfterAllDeletes(t *testing.T) {
	original := linuxRunCommand
	t.Cleanup(func() { linuxRunCommand = original })
	var commands []string
	linuxRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if command == "ip -o -4 route show table main" {
			return "198.51.100.8/32 via 192.0.2.1 dev eth0 proto 233 metric 233\n", nil
		}
		if strings.HasPrefix(command, "ip rule del") {
			return "permission denied", fmt.Errorf("permission denied")
		}
		return "", nil
	}

	err := RecoverLinuxOwnedRoutes(233, 23333, "dobby233")
	if err == nil || !strings.Contains(err.Error(), "remove owned fwmark rule") {
		t.Fatalf("error = %v", err)
	}
	if got := commands[len(commands)-1]; !strings.HasPrefix(got, "ip -4 route del table main") {
		t.Fatalf("route cleanup was not attempted after rule failure: %#v", commands)
	}
}
