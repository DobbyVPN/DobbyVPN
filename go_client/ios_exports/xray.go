package cloak_outline

import (
	log "go_client/logger"
	"go_client/xray"
	"sync"
)

var xrayClient *xray.XrayClient
var xrayMu sync.Mutex

// XrayConnect connects the xray client. Returns error if connection fails.
func XrayConnect() error {
	defer guard("XrayConnect")()
	log.Infof("XrayConnect() called")
	xrayMu.Lock()
	defer xrayMu.Unlock()

	if xrayClient == nil {
		log.Infof("Connect() failed: client is nil")
		return &xrayError{msg: "client is nil"}
	}
	err := xrayClient.Connect()
	if err != nil {
		log.Infof("Connect() failed: %v", err)
		return err
	}
	log.Infof("XrayConnect() finished successfully")
	return nil
}

// XrayDisconnect disconnects the xray client.
func XrayDisconnect() {
	defer guard("XrayDisconnect")()
	log.Infof("XrayDisconnect() called")
	xrayMu.Lock()
	defer xrayMu.Unlock()

	if xrayClient != nil {
		_ = xrayClient.Disconnect()
		xrayClient = nil
	}
	log.Infof("XrayDisconnect() finished")
}

// NewXrayClient creates a new xray client with the given config and file descriptor.
func NewXrayClient(config string, fd int) {
	defer guard("NewXrayClient")()
	log.Infof("NewXrayClient() called")

	xrayMu.Lock()
	defer xrayMu.Unlock()
	// Ensure cleanup of previous instance
	if xrayClient != nil {
		_ = xrayClient.Disconnect()
		xrayClient = nil
	}

	log.Infof("Creating Xray client with FD=%d", fd)

	// Calls the mobile client logic (client_mobile.go)
	xrayClient = xray.NewXrayClient(config, fd)
	log.Infof("NewXrayClient() finished")
}

// xrayError is a simple error type for gomobile
type xrayError struct {
	msg string
}

func (e *xrayError) Error() string {
	return e.msg
}
