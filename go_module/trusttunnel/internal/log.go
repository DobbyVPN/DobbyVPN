package internal

import (
	"github.com/BurntSushi/toml"
	log "go_module/log"
	tt "trusttunnel-go/manager"
)

var logLevel tt.LogLevel

func LogFunc(level tt.LogLevel, message string) {
	if level > logLevel {
		return
	}
	switch level {
	case tt.LogError:
		log.Errorf("[TrustTunnel] %s", message)
	case tt.LogWarn:
		log.Warnf("[TrustTunnel] %s", message)
	case tt.LogInfo:
		log.Infof("[TrustTunnel] %s", message)
	case tt.LogDebug:
		log.Debugf("[TrustTunnel] %s", message)
	default:
		log.Debugf("[TrustTunnel] %s", message)
	}
}

func SetLogLever(level tt.LogLevel) {
	logLevel = level
}

func ExtractLogLevel(configStr string) (tt.LogLevel, error) {
	var cfg struct {
		LogLevel string `toml:"loglevel"`
	}
	if _, err := toml.Decode(configStr, &cfg); err != nil {
		return tt.LogInfo, err
	}

	switch cfg.LogLevel {
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
