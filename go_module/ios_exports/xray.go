package cloak_outline

import (
	"sync"

	"go_module/core"
	log "go_module/log"
	"go_module/xray"
)

var xrayClient *core.CoreClient
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
func NewXrayClient(config string) {
	defer guard("NewXrayClient")()
	log.Infof("NewXrayClient() called")

	xrayMu.Lock()
	defer xrayMu.Unlock()
	// Ensure cleanup of previous instance
	if xrayClient != nil {
		_ = xrayClient.Disconnect()
		xrayClient = nil
	}

	log.Infof("Start fd search")

	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		log.Infof("NewOutlineClient(): utun fd not found")
		return
	}

	log.Infof("Fd was found, fd = %d", fd)
	log.Infof("Config length=%d", len(config))

	// Calls the mobile client logic (client_mobile.go)
	device, err := xray.NewXrayDevice(config)
	if err != nil {
		log.Errorf("Failed to create xray device: %v", err)
		return
	}
	xrayClient = core.NewClient(device, fd)
	log.Infof("NewXrayClient() finished")
}

// xrayError is a simple error type for gomobile
type xrayError struct {
	msg string
}

func (e *xrayError) Error() string {
	return e.msg
}
