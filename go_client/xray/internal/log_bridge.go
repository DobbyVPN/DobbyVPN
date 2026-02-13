package internal

import (
	appLog "go_client/logger"

	xrayLog "github.com/xtls/xray-core/common/log"
)

// SetupXrayLogging initializes the log redirection for xray-core.
func SetupXrayLogging(logLevel xrayLog.Severity) {
	appLog.Infof("Start xray's logging setup")
	xrayLog.RegisterHandler(&xrayLogBridge{logLevel: logLevel})
	appLog.Infof("End xray's logging setup")
}

type xrayLogBridge struct {
	logLevel xrayLog.Severity
}

func (l *xrayLogBridge) Handle(msg xrayLog.Message) {
	switch msg := msg.(type) {
	case *xrayLog.GeneralMessage:
		if msg.Severity <= l.logLevel {
			appLog.Infof("[Xray-Core] %s", msg.String())
		}
	default:
		appLog.Infof("[Xray-Core] %s", msg.String())
	}
}
