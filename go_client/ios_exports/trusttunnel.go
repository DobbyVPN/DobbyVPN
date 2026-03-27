package cloak_outline

import (
	log "go_client/logger"
	"go_client/trusttunnel"
	"sync"
)

var trustTunnelClient *trusttunnel.TrustTunnelClient
var trustTunnelMu sync.Mutex

// TrustTunnelConnect connects the TrustTunnel client. Returns error if connection fails.
func TrustTunnelConnect() error {
	defer guard("TrustTunnelConnect")()
	log.Infof("TrustTunnelConnect() called")
	trustTunnelMu.Lock()
	defer trustTunnelMu.Unlock()

	if trustTunnelClient == nil {
		log.Infof("Connect() failed: client is nil")
		return &trustTunnelError{msg: "client is nil"}
	}
	err := trustTunnelClient.Connect()
	if err != nil {
		log.Infof("Connect() failed: %v", err)
		return err
	}
	log.Infof("TrustTunnelConnect() finished successfully")
	return nil
}

// TrustTunnelDisconnect disconnects the TrustTunnel client.
func TrustTunnelDisconnect() {
	defer guard("TrustTunnelDisconnect")()
	log.Infof("TrustTunnelDisconnect() called")
	trustTunnelMu.Lock()
	defer trustTunnelMu.Unlock()

	if trustTunnelClient != nil {
		_ = trustTunnelClient.Disconnect()
		trustTunnelClient = nil
	}
	log.Infof("TrustTunnelDisconnect() finished")
}

// NewTrustTunnelClient creates a new TrustTunnel client with the given config and file descriptor.
func NewTrustTunnelClient(config string, fd int) {
	defer guard("NewTrustTunnelClient")()
	log.Infof("NewTrustTunnelClient() called")

	trustTunnelMu.Lock()
	defer trustTunnelMu.Unlock()
	// Ensure cleanup of previous instance
	if trustTunnelClient != nil {
		_ = trustTunnelClient.Disconnect()
		trustTunnelClient = nil
	}

	log.Infof("Creating TrustTunnel client with FD=%d", fd)

	// Calls the mobile client logic (client_mobile.go)
	trustTunnelClient = trusttunnel.NewTrustTunnelClient(config, fd)
	log.Infof("NewTrustTunnelClient() finished")
}

// trustTunnelError is a simple error type for gomobile
type trustTunnelError struct {
	msg string
}

func (e *trustTunnelError) Error() string {
	return e.msg
}
