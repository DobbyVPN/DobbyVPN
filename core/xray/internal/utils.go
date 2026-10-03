package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	xrayLog "github.com/xtls/xray-core/common/log"

	"core/dnscache"
)

const (
	defaultXrayLogLevelName = "debug"
	xrayLogLevelInfo        = "info"
	xrayLogLevelWarning     = "warning"
	xrayLogLevelError       = "error"
	xrayStreamSecurityTLS   = "tls"
)

func DefaultXrayLogLevel() xrayLog.Severity {
	return xrayLog.Severity_Debug
}

func NoXrayLogLevel() xrayLog.Severity {
	return xrayLog.Severity_Unknown
}

func XrayLogLevelName(level xrayLog.Severity) string {
	switch level {
	case xrayLog.Severity_Debug:
		return defaultXrayLogLevelName
	case xrayLog.Severity_Info:
		return xrayLogLevelInfo
	case xrayLog.Severity_Warning:
		return xrayLogLevelWarning
	case xrayLog.Severity_Error:
		return xrayLogLevelError
	case xrayLog.Severity_Unknown:
		return "none"
	default:
		return fmt.Sprintf("unknown(%d)", level)
	}
}

// ExtractServerIP resolves the accepted configuration for this attempt.
func ExtractServerIP(config map[string]any, dnsCache *dnscache.Cache) (string, error) {
	if dnsCache == nil {
		return "", errors.New("DNS cache is required")
	}

	// Assuming standard Xray config structure where outbound[0] is the proxy
	address, ok := firstXrayServerAddress(config)
	if ok {
		return resolveIP(address, dnsCache)
	}
	return "", errors.New("could not find server address in config")
}

func firstXrayServerAddress(config map[string]interface{}) (string, bool) {
	outbounds, ok := config["outbounds"].([]interface{})
	if !ok || len(outbounds) == 0 {
		return "", false
	}
	firstOut, ok := outbounds[0].(map[string]interface{})
	if !ok {
		return "", false
	}
	settings, ok := firstOut["settings"].(map[string]interface{})
	if !ok {
		return "", false
	}
	vnext, ok := settings["vnext"].([]interface{})
	if !ok || len(vnext) == 0 {
		return "", false
	}
	server, ok := vnext[0].(map[string]interface{})
	if !ok {
		return "", false
	}
	address, ok := server["address"].(string)
	return address, ok
}

// ExtractLogLevel reads the accepted configuration.
// In error case returns xrayLog.Severity_Unknown
func ExtractLogLevel(config map[string]any) (xrayLog.Severity, error) {

	// Assuming standard Xray config structure where log[0] is the log settings
	if log, ok := config["log"].(map[string]interface{}); ok && len(log) > 0 {
		if loglevel, ok := log["loglevel"].(string); ok {
			switch strings.ToLower(loglevel) {
			case defaultXrayLogLevelName:
				return xrayLog.Severity_Debug, nil
			case xrayLogLevelInfo:
				return xrayLog.Severity_Info, nil
			case xrayLogLevelWarning:
				return xrayLog.Severity_Warning, nil
			case xrayLogLevelError:
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
func resolveIP(addr string, dnsCache *dnscache.Cache) (string, error) {
	ip := net.ParseIP(addr)
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
		return "", errors.New("IPv6 address not supported; routing requires IPv4")
	}

	ip4, err := dnsCache.ResolvePreflightIPv4(context.Background(), addr, dnscache.ServerResolveTimeout, "xray")
	if err != nil {
		return "", fmt.Errorf("failed to resolve xray address %q: %w", addr, err)
	}
	return ip4.String(), nil
}
