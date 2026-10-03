package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// ExtractServerIP resolves the first endpoint address at the attempt boundary.
func ExtractServerIP(config map[string]any) (string, error) {
	endpoint, ok := config["endpoint"].(map[string]any)
	if !ok {
		return "", errors.New("invalid endpoint configuration")
	}
	addresses, ok := endpoint["addresses"].([]any)
	if !ok || len(addresses) == 0 {
		return "", errors.New("no addresses found in endpoint configuration")
	}
	address, ok := addresses[0].(string)
	if !ok {
		return "", errors.New("endpoint address is not a string")
	}
	return resolveIP(address)
}

func resolveIP(addr string) (string, error) {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}

	ip := net.ParseIP(host)
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
		return "", errors.New("IPv6 address not supported; routing requires IPv4")
	}

	// If it's a domain, resolve it
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ips, err := (&net.Resolver{}).LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve trusttunnel address %q: %w", host, err)
	}
	for _, ip := range ips {
		if ip4 := ip.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", errors.New("no IPv4 address found for domain")
}
