package cloak_outline

import (
<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
	"go_module/log"
=======
=======
	log "go_module/log"
	"go_module/xray"
	"os"
>>>>>>> ad8d9c92 (make core module in go_module that unifies work with protocols, fix HeathCheck on desktop)
=======
	"os"
>>>>>>> 45894f1a (fix ios fd int to os.File)
	"sync"

	log "go_module/log"
>>>>>>> 4364bc5c (Add xray ios part)
	"go_module/xray"
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
	tunFile := os.NewFile(uintptr(fd), "utun")

	log.Infof("Fd was found, fd = %d", fd)
	log.Infof("Config length=%d", len(config))

	tunFile := os.NewFile(uintptr(fd), "utun")

	var err error
	xrayClient, err = xray.NewXrayClient(config, tunFile)
	if err != nil {
		log.Errorf("Failed to create xray client: %v", err)
		xrayClient = nil
		return
	}
<<<<<<< HEAD
=======
	xrayClient = core.NewClient(device, tunFile)
>>>>>>> 45894f1a (fix ios fd int to os.File)
	log.Infof("NewXrayClient() finished")
}

// xrayError is a simple error type for gomobile
type xrayError struct {
	msg string
}

func (e *xrayError) Error() string {
	return e.msg
}
