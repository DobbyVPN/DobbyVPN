package internal

import (
	"encoding/json"
	"errors"
	"net"
)

// ExtractServerIP parses the generic VLESS JSON to find the remote server IP.
func ExtractServerIP(configStr string) (string, error) {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return "", err
	}

	// Assuming standard Xray config structure where outbound[0] is the proxy
	if outbounds, ok := config["outbounds"].([]interface{}); ok && len(outbounds) > 0 {
		if firstOut, ok := outbounds[0].(map[string]interface{}); ok {
			if settings, ok := firstOut["settings"].(map[string]interface{}); ok {
				if vnext, ok := settings["vnext"].([]interface{}); ok && len(vnext) > 0 {
					if server, ok := vnext[0].(map[string]interface{}); ok {
						if address, ok := server["address"].(string); ok {
							return resolveIP(address)
						}
					}
				}
			}
		}
	}
	return "", errors.New("could not find server address in config")
}

// resolveIP resolves a domain to an IP, or returns the IP if it's already one.
func resolveIP(addr string) (string, error) {
	ip := net.ParseIP(addr)
	if ip != nil {
		return ip.String(), nil
	}

	// If it's a domain, resolve it
	ips, err := net.LookupIP(addr)
	if err != nil {
		return "", err
	}
	if len(ips) == 0 {
		return "", errors.New("no IP found for domain")
	}
	return ips[0].String(), nil // Use the first resolved IP
}
