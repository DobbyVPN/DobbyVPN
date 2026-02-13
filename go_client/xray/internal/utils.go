package internal

import (
	"encoding/json"
	"errors"
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
				return xrayLog.Severity_Unknown, errors.New("log level is not presented, choose between debug|info|warning|error|none")
			}
		}
	}
	return xrayLog.Severity_Unknown, errors.New("could not find log level in config")
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
