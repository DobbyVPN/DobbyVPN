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
// answers before the native SDK receives the configuration. TLS hostname
// and credential fields remain unchanged.
func PrepareEndpointAddresses(ctx context.Context, endpoint map[string]any, lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)) ([]any, net.IP, error) {
	addresses, ok := endpoint["addresses"].([]any)
	if !ok || len(addresses) == 0 {
		return nil, nil, errors.New("no addresses found in endpoint configuration")
	}
	if lookupIPAddr == nil {
		return nil, nil, errors.New("underlying resolver is required")
	}

	ctx, cancel := context.WithTimeout(ctx, endpointResolutionTimeout)
	defer cancel()

	prepared := make([]any, 0, len(addresses))
	var primaryIP net.IP
	for index, value := range addresses {
		address, ok := value.(string)
		if !ok {
			return nil, nil, fmt.Errorf("endpoint address[%d] is not a string", index)
		}
		resolved, addressIP, err := prepareEndpointAddress(ctx, index, address, lookupIPAddr)
		if err != nil {
			return nil, nil, err
		}
		prepared = append(prepared, resolved...)
		if index == 0 {
			primaryIP = addressIP
		}
	}

	return prepared, primaryIP, nil
}

func prepareEndpointAddress(ctx context.Context, index int, address string, lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)) ([]any, net.IP, error) {
	host, port, hasPort, literal, err := splitEndpointAddress(address)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid endpoint address[%d] %q: %w", index, address, err)
	}
	if literal.IsValid() {
		primaryIP := net.IP(literal.AsSlice())
		return []any{address}, primaryIP, nil
	}

	resolved, err := lookupIPAddr(ctx, host)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to resolve endpoint address[%d] %q (host %q): %w", index, address, host, err)
	}
	if len(resolved) == 0 {
		return nil, nil, fmt.Errorf("endpoint address[%d] %q (host %q) resolved to no addresses", index, address, host)
	}

	prepared, addressIP, err := prepareResolvedEndpointAddresses(resolved, port, hasPort)
	if err != nil {
		return nil, nil, fmt.Errorf("endpoint address[%d] %q (host %q) resolved to an invalid IP address", index, address, host)
	}
	return prepared, addressIP, nil
}

func prepareResolvedEndpointAddresses(resolved []net.IPAddr, port string, hasPort bool) ([]any, net.IP, error) {
	prepared := make([]any, 0, len(resolved))
	var addressIP net.IP
	for _, result := range resolved {
		resolvedAddress, ip, err := resolvedEndpointAddress(result)
		if err != nil {
			return nil, nil, err
		}
		if addressIP == nil {
			addressIP = ip
		}
		if hasPort {
			resolvedAddress = net.JoinHostPort(resolvedAddress, port)
		}
		prepared = append(prepared, resolvedAddress)
	}
	return prepared, addressIP, nil
}

func resolvedEndpointAddress(result net.IPAddr) (string, net.IP, error) {
	if ip4 := result.IP.To4(); ip4 != nil {
		return ip4.String(), append(net.IP(nil), ip4...), nil
	}
	ip, ok := netip.AddrFromSlice(result.IP)
	if !ok {
		return "", nil, errors.New("invalid IP address")
	}
	if result.Zone != "" && ip.Is6() {
		ip = ip.WithZone(result.Zone)
	}
	return ip.String(), append(net.IP(nil), result.IP...), nil
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
