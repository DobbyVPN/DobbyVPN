//go:build darwin && !(android || ios)

package routing

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	macOSPhysicalHostRoute = "route to: 198.51.100.8\ndestination: 198.51.100.8\ngateway: 192.0.2.1\ninterface: en0\n"
	macOSPhysicalFallback  = "route to: 198.51.100.8\ndestination: default\ngateway: 192.0.2.1\ninterface: en0\n"
	macOSTunnelFallback    = "route to: 198.51.100.8\ndestination: 128.0.0.0\ninterface: utun8\n"
)

func TestMacOSIPv4DefaultLeavesPhysicalDefaultUnchanged(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		return "", nil
	}

	plan := NewPlan("generation-31")
	if err := plan.AcquireMacOSIPv4Default("utun8"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n add -net 0.0.0.0/1 -interface utun8",
		"route -n add -net 128.0.0.0/1 -interface utun8",
		"route -n delete -net 128.0.0.0/1 -interface utun8",
		"route -n delete -net 0.0.0.0/1 -interface utun8",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
	for _, command := range commands {
		if strings.Contains(command, " default") {
			t.Fatalf("physical default route was touched: %q", command)
		}
	}
}

func TestMacOSIPv6LeasesAdoptRoutesLeftByKilledPredecessor(t *testing.T) {
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

	plan := NewPlan("generation-32")
	if err := plan.AcquireMacOSIPv6Block("utun8"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n add -inet6 -net ::/1 -interface utun8",
		"route -n add -inet6 -net 8000::/1 -interface utun8",
		"route -n delete -inet6 -net 8000::/1 -interface utun8",
		"route -n delete -inet6 -net ::/1 -interface utun8",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestMacOSRoutingFailureRollsBackOnlyResourcesAlreadyAcquired(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		if strings.Contains(command, "128.0.0.0/1") && strings.Contains(command, "route -n add") {
			return "", fmt.Errorf("permission denied")
		}
		return "", nil
	}

	plan := NewPlan("generation-33")
	if err := plan.AcquireMacOSIPv4Default("utun8"); err == nil {
		t.Fatal("AcquireMacOSIPv4Default succeeded")
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n add -net 0.0.0.0/1 -interface utun8",
		"route -n add -net 128.0.0.0/1 -interface utun8",
		"route -n delete -net 0.0.0.0/1 -interface utun8",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestMacOSProxyLeaseAdoptsRouteLeftByKilledPredecessor(t *testing.T) {
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

	plan := NewPlan("generation-34")
	if _, err := plan.AcquireMacOSProxyRoute("198.51.100.8", "192.0.2.1"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"route -n add -host 198.51.100.8 192.0.2.1",
		"route -n delete -host 198.51.100.8 192.0.2.1",
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestRepairMacOSSessionRoutesRestoresFallbackRoutes(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	for _, fallback := range []string{macOSPhysicalFallback, macOSTunnelFallback} {
		fallback := fallback
		t.Run(strings.TrimSpace(strings.Split(fallback, "\n")[2]), func(t *testing.T) {
			var commands []string
			macosRunCommand = func(command string) (string, error) {
				commands = append(commands, command)
				if strings.HasPrefix(command, "route -n get") {
					return fallback, nil
				}
				return "", nil
			}

			repaired, err := RepairMacOSSessionRoutes("198.51.100.8", "192.0.2.1", "utun8", "en0")
			if err != nil {
				t.Fatal(err)
			}
			if !repaired {
				t.Fatal("fallback routes were not reported repaired")
			}
			want := []string{
				"route -n get 198.51.100.8",
				"route -n delete -net 0.0.0.0/1 -interface utun8",
				"route -n delete -net 128.0.0.0/1 -interface utun8",
				"route -n delete -host 198.51.100.8 192.0.2.1",
				"route -n add -host 198.51.100.8 192.0.2.1",
				"route -n add -net 0.0.0.0/1 -interface utun8",
				"route -n add -net 128.0.0.0/1 -interface utun8",
			}
			if !reflect.DeepEqual(commands, want) {
				t.Fatalf("commands = %v, want %v", commands, want)
			}
		})
	}
}

func TestRepairMacOSSessionRoutesAcceptsExistingRestoredRoutes(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	macosRunCommand = func(command string) (string, error) {
		if strings.HasPrefix(command, "route -n get") {
			return macOSTunnelFallback, nil
		}
		if strings.Contains(command, "delete") {
			return "not in table", fmt.Errorf("not in table")
		}
		return "File exists", fmt.Errorf("File exists")
	}

	repaired, err := RepairMacOSSessionRoutes("198.51.100.8", "192.0.2.1", "utun8", "en0")
	if err != nil {
		t.Fatal(err)
	}
	if !repaired {
		t.Fatal("existing restored routes were not accepted")
	}
}

func TestRepairMacOSSessionRoutesWaitsForAnyRoute(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	for _, routeErr := range []error{fmt.Errorf("not in table"), nil} {
		routeErr := routeErr
		t.Run(fmt.Sprint(routeErr), func(t *testing.T) {
			var commands []string
			macosRunCommand = func(command string) (string, error) {
				commands = append(commands, command)
				return "route: writing to routing socket: not in table\n", routeErr
			}

			repaired, err := RepairMacOSSessionRoutes("198.51.100.8", "192.0.2.1", "utun8", "en0")
			if err != nil {
				t.Fatal(err)
			}
			if repaired {
				t.Fatal("routes were reported repaired before any route returned")
			}
			if want := []string{"route -n get 198.51.100.8"}; !reflect.DeepEqual(commands, want) {
				t.Fatalf("commands = %v, want %v", commands, want)
			}
		})
	}
}

func TestRepairMacOSSessionRoutesLeavesExactHostRouteAlone(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	var commands []string
	macosRunCommand = func(command string) (string, error) {
		commands = append(commands, command)
		return macOSPhysicalHostRoute, nil
	}

	repaired, err := RepairMacOSSessionRoutes("198.51.100.8", "192.0.2.1", "utun8", "en0")
	if err != nil {
		t.Fatal(err)
	}
	if repaired {
		t.Fatal("healthy exact host route was reported repaired")
	}
	if want := []string{"route -n get 198.51.100.8"}; !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %v, want %v", commands, want)
	}
}

func TestRepairMacOSSessionRoutesPreservesMalformedRouteError(t *testing.T) {
	original := macosRunCommand
	t.Cleanup(func() { macosRunCommand = original })
	macosRunCommand = func(string) (string, error) {
		return "route to: 198.51.100.8\ndestination: 198.51.100.8\n", nil
	}

	_, err := RepairMacOSSessionRoutes("198.51.100.8", "192.0.2.1", "utun8", "en0")
	if err == nil || !strings.Contains(err.Error(), "route has no interface") {
		t.Fatalf("error = %v", err)
	}
}
