package internal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	xrayLog "github.com/xtls/xray-core/common/log"

	"go_module/dnscache"
)

const xrayResolveTimeout = 2 * time.Second
const defaultXrayLogLevelName = "debug"

func DefaultXrayLogLevel() xrayLog.Severity {
	return xrayLog.Severity_Debug
}

func NoXrayLogLevel() xrayLog.Severity {
	return xrayLog.Severity_Unknown
}

func XrayLogLevelName(level xrayLog.Severity) string {
	switch level {
	case xrayLog.Severity_Debug:
		return "debug"
	case xrayLog.Severity_Info:
		return "info"
	case xrayLog.Severity_Warning:
		return "warning"
	case xrayLog.Severity_Error:
		return "error"
	case xrayLog.Severity_Unknown:
		return "none"
	default:
		return fmt.Sprintf("unknown(%d)", level)
	}
}

// ExtractServerIP parses the generic VLESS JSON to find the remote server IP.
func ExtractServerIP(configStr string) (string, error) {
	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configStr), &config); err != nil {
		return "", fmt.Errorf("failed to unmarshal xray config while extracting server IP: %w", err)
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
		return xrayLog.Severity_Unknown, fmt.Errorf("failed to unmarshal xray config while extracting log level: %w", err)
	}
	// Assuming standard Xray config structure where log[0] is the log settings
	if log, ok := config["log"].(map[string]interface{}); ok && len(log) > 0 {
		if loglevel, ok := log["loglevel"].(string); ok {
			switch strings.ToLower(loglevel) {
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

	ip4, err := dnscache.ResolveIPv4(context.Background(), addr, xrayResolveTimeout, "xray")
	if err != nil {
		return "", fmt.Errorf("failed to resolve xray address %q: %w", addr, err)
	}
	return ip4.String(), nil
}
