package internal

import (
	"sync"

	appLog "go_module/log"
	"go_module/xray/common"

	xrayAppLog "github.com/xtls/xray-core/app/log"
	xrayLog "github.com/xtls/xray-core/common/log"
)

var (
	xrayLogBridgeMu      sync.Mutex
	registeredXrayBridge *xrayLogBridge
	xrayLogCreatorSet    bool
)

// SetupXrayLogging initializes the log redirection for xray-core.
func SetupXrayLogging(logLevel xrayLog.Severity) {
	xrayLogBridgeMu.Lock()
	defer xrayLogBridgeMu.Unlock()

	if registeredXrayBridge != nil {
		registeredXrayBridge.logLevel = logLevel
		appLog.Infof(common.Category, "Updated xray logging level=%s", XrayLogLevelName(logLevel))
		return
	}

	appLog.Infof(common.Category, "Start xray's logging setup level=%s", XrayLogLevelName(logLevel))
	registeredXrayBridge = &xrayLogBridge{logLevel: logLevel}
	xrayLog.RegisterHandler(registeredXrayBridge)
	registerXrayConsoleLogCreatorLocked()
	appLog.Infof(common.Category, "End xray's logging setup")
}

func registerXrayConsoleLogCreatorLocked() {
	if xrayLogCreatorSet {
		return
	}
	if err := xrayAppLog.RegisterHandlerCreator(xrayAppLog.LogType_Console, func(xrayAppLog.LogType, xrayAppLog.HandlerCreatorOptions) (xrayLog.Handler, error) {
		return registeredXrayBridge, nil
	}); err != nil {
		appLog.Warnf(common.Category, "failed to register xray console log bridge: %v", err)
		return
	}
	xrayLogCreatorSet = true
	appLog.Infof(common.Category, "Registered xray console log bridge")
}

type xrayLogBridge struct {
	logLevel xrayLog.Severity
}

func (l *xrayLogBridge) Handle(msg xrayLog.Message) {
	switch msg := msg.(type) {
	case *xrayLog.AccessMessage:
		appLog.Infof("Xray-Core", "%s", msg.String())
	case *xrayLog.DNSLog:
		appLog.Debugf("Xray-Core", "%s", msg.String())
	case *xrayLog.GeneralMessage:
		if msg.Severity <= l.logLevel {
			switch msg.Severity {
			case xrayLog.Severity_Debug:
				appLog.Debugf("Xray-Core", "%s", msg.Content)
			case xrayLog.Severity_Info:
				appLog.Infof("Xray-Core", "%s", msg.Content)
			case xrayLog.Severity_Warning:
				appLog.Warnf("Xray-Core", "%s", msg.Content)
			case xrayLog.Severity_Error:
				appLog.Errorf("Xray-Core", "%s", msg.Content)
			default:
				appLog.Infof("Xray-Core", "%s", msg.Content)
			}
		}
	default:
		appLog.Infof("Xray-Core", "%s", msg.String())
	}
}
