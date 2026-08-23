//go:build linux && !(android || ios)

package routing

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"go_module/log"
)

const (
	linuxOwnedRouteProtocol = 233
	linuxOwnedProxyMetric   = 233
)

func linuxRouteField(fields []string, name string) (string, bool) {
	for index := 0; index+1 < len(fields); index++ {
		if fields[index] == name {
			return fields[index+1], true
		}
	}
	return "", false
}

func linuxOwnedProxyRouteDelete(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 9 {
		return "", false
	}
	destination := fields[0]
	_, network, err := net.ParseCIDR(destination)
	if err != nil {
		parsed := net.ParseIP(destination)
		if parsed == nil || parsed.To4() == nil {
			return "", false
		}
	} else if ones, bits := network.Mask.Size(); bits != 32 || ones != 32 {
		return "", false
	}
	gateway, hasGateway := linuxRouteField(fields, "via")
	iface, hasIface := linuxRouteField(fields, "dev")
	protocol, hasProtocol := linuxRouteField(fields, "proto")
	metric, hasMetric := linuxRouteField(fields, "metric")
	if !hasGateway || net.ParseIP(gateway).To4() == nil || !hasIface || iface == "" ||
		!hasProtocol || protocol != strconv.Itoa(linuxOwnedRouteProtocol) ||
		!hasMetric || metric != strconv.Itoa(linuxOwnedProxyMetric) {
		return "", false
	}
	return fmt.Sprintf(
		"ip -4 route del table main %s via %s dev %s proto %d metric %d",
		destination, gateway, iface, linuxOwnedRouteProtocol, linuxOwnedProxyMetric,
	), true
}

func linuxOwnedMarkedRouteDelete(line string, tableID int) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 || fields[0] != "default" {
		return "", false
	}
	gateway, hasGateway := linuxRouteField(fields, "via")
	iface, hasIface := linuxRouteField(fields, "dev")
	protocol, hasProtocol := linuxRouteField(fields, "proto")
	if !hasGateway || net.ParseIP(gateway).To4() == nil || !hasIface || iface == "" ||
		!hasProtocol || protocol != strconv.Itoa(linuxOwnedRouteProtocol) {
		return "", false
	}
	return fmt.Sprintf(
		"ip -4 route del table %d default via %s dev %s proto %d",
		tableID, gateway, iface, linuxOwnedRouteProtocol,
	), true
}

func linuxOwnedTunnelRouteDelete(line, tunName string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "default" {
		return "", false
	}
	iface, hasIface := linuxRouteField(fields, "dev")
	protocol, hasProtocol := linuxRouteField(fields, "proto")
	if !hasIface || iface != tunName || !hasProtocol || protocol != strconv.Itoa(linuxOwnedRouteProtocol) {
		return "", false
	}
	return fmt.Sprintf(
		"ip -4 route del table main default dev %s proto %d",
		tunName, linuxOwnedRouteProtocol,
	), true
}

func linuxOwnedIPv6RouteDelete(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 6 || fields[0] != "blackhole" ||
		(fields[1] != ipv6LowerHalf && fields[1] != ipv6UpperHalf) {
		return "", false
	}
	protocol, hasProtocol := linuxRouteField(fields, "proto")
	metric, hasMetric := linuxRouteField(fields, "metric")
	if !hasProtocol || protocol != strconv.Itoa(linuxOwnedRouteProtocol) || !hasMetric || metric != "1" {
		return "", false
	}
	return fmt.Sprintf(
		"ip -6 route del table main blackhole %s proto %d metric 1",
		fields[1], linuxOwnedRouteProtocol,
	), true
}

func linuxRoutingTableAbsent(output string, err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(output+"\n"+err.Error()), "fib table does not exist")
}

// RecoverLinuxOwnedRoutes removes only routes carrying DobbyVPN's explicit
// protocol/metric ownership tags. The kernel retains routes and rules when the
// service is killed, so the next process must reclaim those tagged resources
// before creating a new routing Plan. Untagged pre-existing routes remain
// outside product ownership and are never removed.
func RecoverLinuxOwnedRoutes(tableID, priority int, tunName string) error {
	// Keep these queries unfiltered: iproute2 omits the protocol field from
	// output when a protocol selector is present, and recovery must independently
	// verify the ownership tag before deleting anything.
	mainOutput, err := linuxRunCommand("ip -o -4 route show table main")
	if err != nil {
		return fmt.Errorf("inspect owned main-table routes: %w", err)
	}
	markedOutput, err := linuxRunCommand(fmt.Sprintf(
		"ip -o -4 route show table %d", tableID,
	))
	if linuxRoutingTableAbsent(markedOutput, err) {
		markedOutput = ""
		err = nil
	} else if err != nil {
		return fmt.Errorf("inspect owned marked-table routes: %w", err)
	}
	ipv6Output, err := linuxRunCommand("ip -o -6 route show table main")
	if err != nil {
		return fmt.Errorf("inspect owned IPv6 routes: %w", err)
	}

	var deletes []string
	for _, line := range strings.Split(mainOutput, "\n") {
		if command, ok := linuxOwnedProxyRouteDelete(line); ok {
			deletes = append(deletes, command)
			continue
		}
		if command, ok := linuxOwnedTunnelRouteDelete(line, tunName); ok {
			deletes = append(deletes, command)
		}
	}
	for _, line := range strings.Split(markedOutput, "\n") {
		if command, ok := linuxOwnedMarkedRouteDelete(line, tableID); ok {
			deletes = append(deletes, command)
		}
	}
	for _, line := range strings.Split(ipv6Output, "\n") {
		if command, ok := linuxOwnedIPv6RouteDelete(line); ok {
			deletes = append(deletes, command)
		}
	}
	if len(deletes) == 0 {
		return nil
	}

	log.Debugf(Category, "[Linux][Recovery] removing %d tagged route(s) after process loss", len(deletes))
	var errs []error
	ruleCommand := fmt.Sprintf("ip rule del fwmark %d lookup %d priority %d", tableID, tableID, priority)
	if _, ruleErr := linuxRunCommand(ruleCommand); ruleErr != nil && !linuxRouteAlreadyGone(ruleErr) {
		errs = append(errs, fmt.Errorf("remove owned fwmark rule: %w", ruleErr))
	}
	for _, command := range deletes {
		if _, deleteErr := linuxRunCommand(command); deleteErr != nil && !linuxRouteAlreadyGone(deleteErr) {
			errs = append(errs, fmt.Errorf("remove owned route: %w", deleteErr))
		}
	}
	return errors.Join(errs...)
}
