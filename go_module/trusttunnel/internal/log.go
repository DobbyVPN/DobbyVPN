package internal

import (
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

func ExtractLogLevel(config string) (tt.LogLevel, error) {
	return tt.LogInfo, nil
}
