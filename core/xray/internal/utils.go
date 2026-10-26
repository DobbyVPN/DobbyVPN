package internal

import (
	"errors"
	"fmt"
	"strings"

	xrayLog "github.com/xtls/xray-core/common/log"
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

func ExtractLogLevel(config map[string]any) (xrayLog.Severity, error) {
	if config["log"] == nil {
		return DefaultXrayLogLevel(), nil
	}
	if log, ok := config["log"].(map[string]interface{}); ok {
		if log["loglevel"] == nil {
			return DefaultXrayLogLevel(), nil
		}
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
