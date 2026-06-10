package internal

import (
	"errors"
	"fmt"
	"net"

	"github.com/BurntSushi/toml"
)

// Config represents the TOML configuration structure for trusttunnel.
type Config struct {
	Endpoint EndpointConfig `toml:"endpoint"`
}

// EndpointConfig represents the [endpoint] section of the TOML config.
type EndpointConfig struct {
	Hostname         string   `toml:"hostname"`
	Addresses        []string `toml:"addresses"`
	Username         string   `toml:"username"`
	Password         string   `toml:"password"`
	UpstreamProtocol string   `toml:"upstream_protocol"`
}

// ExtractServerIP parses the TOML config and extracts the first server IP address
// from the endpoint.addresses array. If the address is a domain name, it resolves
// it to an IPv4 address. Returns an error if the config is invalid or no IPv4
// address can be found.
func ExtractServerIP(configStr string) (string, error) {
	var cfg Config
	if _, err := toml.Decode(configStr, &cfg); err != nil {
		return "", fmt.Errorf("failed to unmarshal trusttunnel config while extracting server IP: %w", err)
	}

	if len(cfg.Endpoint.Addresses) == 0 {
		return "", errors.New("no addresses found in endpoint configuration")
	}

	// Use the first address from the addresses array
	address := cfg.Endpoint.Addresses[0]
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
	ips, err := net.LookupIP(host)
	if err != nil {
		return "", fmt.Errorf("failed to resolve trusttunnel address %q: %w", host, err)
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", errors.New("no IPv4 address found for domain")
}
