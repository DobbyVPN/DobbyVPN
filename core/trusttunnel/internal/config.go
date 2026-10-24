package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
)

const endpointResolutionTimeout = 5 * time.Second

// PrepareEndpointAddresses replaces every DNS name with its ordered numeric
// answers before the native SDK receives the configuration. The first
// configured address must still provide an IPv4 route target.
func PrepareEndpointAddresses(endpoint map[string]any, lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)) ([]any, net.IP, error) {
	addresses, ok := endpoint["addresses"].([]any)
	if !ok || len(addresses) == 0 {
		return nil, nil, errors.New("no addresses found in endpoint configuration")
	}
	if lookupIPAddr == nil {
		lookupIPAddr = (&net.Resolver{}).LookupIPAddr
	}

	ctx, cancel := context.WithTimeout(context.Background(), endpointResolutionTimeout)
	defer cancel()

	prepared := make([]any, 0, len(addresses))
	var primaryIP net.IP
	for index, value := range addresses {
		address, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("endpoint address[%d] is not a string", index)
		}
		host, port, hasPort, literal, err := splitEndpointAddress(address)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid endpoint address[%d] %q: %w", index, address, err)
		}
		if literal.IsValid() {
			if index == 0 {
				primaryIP = ipv4(literal)
				if primaryIP == nil {
					return nil, nil, fmt.Errorf("endpoint address[0] %q: IPv6 address not supported; routing requires IPv4", address)
				}
			}
			prepared = append(prepared, address)
			continue
		}

		resolved, err := lookupIPAddr(ctx, host)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve endpoint address[%d] %q (host %q): %w", index, address, host, err)
		}
		if len(resolved) == 0 {
			return nil, nil, fmt.Errorf("endpoint address[%d] %q (host %q) resolved to no addresses", index, address, host)
		}

		var addressIPv4 net.IP
		for _, result := range resolved {
			resolvedAddress := ""
			if ip4 := result.IP.To4(); ip4 != nil {
				if addressIPv4 == nil {
					addressIPv4 = append(net.IP(nil), ip4...)
				}
				resolvedAddress = ip4.String()
			} else {
				ip, ok := netip.AddrFromSlice(result.IP)
				if !ok {
					return nil, nil, fmt.Errorf("endpoint address[%d] %q (host %q) resolved to an invalid IP address", index, address, host)
				}
				if result.Zone != "" && ip.Is6() {
					ip = ip.WithZone(result.Zone)
				}
				resolvedAddress = ip.String()
			}
			if hasPort {
				resolvedAddress = net.JoinHostPort(resolvedAddress, port)
			}
			prepared = append(prepared, resolvedAddress)
		}
		if index == 0 {
			primaryIP = addressIPv4
			if primaryIP == nil {
				return nil, nil, fmt.Errorf("endpoint address[0] %q: no IPv4 address found for domain", address)
			}
		}
	}

	return prepared, primaryIP, nil
}

func splitEndpointAddress(address string) (host, port string, hasPort bool, literal netip.Addr, err error) {
	host, port, splitErr := net.SplitHostPort(address)
	if splitErr == nil {
		literal, _ = netip.ParseAddr(host)
		return host, port, true, literal, nil
	}
	if literal, err = netip.ParseAddr(address); err == nil {
		return address, "", false, literal, nil
	}
	if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
		host = address[1 : len(address)-1]
		if literal, err = netip.ParseAddr(host); err == nil {
			return host, "", false, literal, nil
		}
	}
	if strings.Contains(address, ":") {
		return "", "", false, netip.Addr{}, fmt.Errorf("expected a valid host:port address: %w", splitErr)
	}
	return address, "", false, netip.Addr{}, nil
}

func ipv4(address netip.Addr) net.IP {
	if !address.Is4() && !address.Is4In6() {
		return nil
	}
	bytes := address.As4()
	return net.IP(bytes[:])
}
