package internal

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	xrayLog "github.com/xtls/xray-core/common/log"
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

// ExtractLogLevel parses the generic VLESS JSON to find the log level.
// In error case returns xrayLog.Severity_Unknown
func ExtractLogLevel(configStr string) (xrayLog.Severity, error) {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return xrayLog.Severity_Unknown, err
	}
	// Assuming standard Xray config structure where log[0] is the log settings
	if log, ok := config["log"].(map[string]interface{}); ok && len(log) > 0 {
		if loglevel, ok := log["loglevel"].(string); ok {
			switch loglevel {
			case "debug":
				return xrayLog.Severity_Debug, nil
			case "info":
				return xrayLog.Severity_Info, nil
			case "warning":
				return xrayLog.Severity_Warning, nil
			case "error":
				return xrayLog.Severity_Error, nil
			case "none":
				return xrayLog.Severity_Unknown, nil
			default:
				return xrayLog.Severity_Unknown, fmt.Errorf("unrecognized log level %q, choose between debug|info|warning|error|none", loglevel)
			}
		}
	}
	return xrayLog.Severity_Unknown, errors.New("could not find log level in config")
}

// resolveIP resolves a domain to an IP, or returns the IP if it's already one.
func resolveIP(addr string) (string, error) {
	ip := net.ParseIP(addr)
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
		return "", errors.New("IPv6 address not supported; routing requires IPv4")
	}

	// If it's a domain, resolve it
	ips, err := net.LookupIP(addr)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", errors.New("no IPv4 address found for domain")
}
