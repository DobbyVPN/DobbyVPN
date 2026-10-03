package internal

import (
	"fmt"
	"sync/atomic"

	log "core/log"
	tt "trusttunnel-go/manager"
)

var logLevel atomic.Int64

func LogFunc(level tt.LogLevel, message string) {
	if int64(level) > logLevel.Load() {
		return
	}
	switch level {
	case tt.LogError:
		log.Errorf("trusttunnel", "[TrustTunnel] %s", message)
	case tt.LogWarn:
		log.Warnf("trusttunnel", "[TrustTunnel] %s", message)
	case tt.LogInfo:
		log.Infof("trusttunnel", "[TrustTunnel] %s", message)
	case tt.LogDebug, tt.LogTrace:
		log.Debugf("trusttunnel", "[TrustTunnel] %s", message)
	default:
		log.Debugf("trusttunnel", "[TrustTunnel] %s", message)
	}
}

func SetLogLevel(level tt.LogLevel) {
	logLevel.Store(int64(level))
}

func ExtractLogLevel(config map[string]any) (tt.LogLevel, error) {
	level, ok := config["loglevel"].(string)
	if !ok && config["loglevel"] != nil {
		return tt.LogInfo, fmt.Errorf("invalid TrustTunnel loglevel: %v", config["loglevel"])
	}

	switch level {
	case "debug":
		return tt.LogDebug, nil
	case "info":
		return tt.LogInfo, nil
	case "warn", "warning":
		return tt.LogWarn, nil
	case "error":
		return tt.LogError, nil
	default:
		return tt.LogInfo, nil
	}
}
