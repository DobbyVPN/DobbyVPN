package main

/*
#include <stdlib.h>
*/
import "C"
import (
	log "go_client/logger"
	"go_client/trusttunnel"
	"sync"
)

var trusttunnelClient *trusttunnel.TrustTunnelClient
var trusttunnelMu sync.Mutex

//export TrustTunnelConnect
func TrustTunnelConnect() C.int {
	trusttunnelMu.Lock()
	defer trusttunnelMu.Unlock()
	log.Infof("TrustTunnelConnect() called")

	if trusttunnelClient == nil {
		log.Infof("Connect() failed: client is nil")
		return -1
	}
	err := trusttunnelClient.Connect()
	if err != nil {
		log.Infof("Connect() failed: %v", err)
		return -1
	}
	return 0
}

//export TrustTunnelDisconnect
func TrustTunnelDisconnect() {
	trusttunnelMu.Lock()
	defer trusttunnelMu.Unlock()
	log.Infof("TrustTunnelDisconnect() called")

	if trusttunnelClient != nil {
		_ = trusttunnelClient.Disconnect()
	}
}

//export NewTrustTunnelClient
func NewTrustTunnelClient(config *C.char, fd C.int) {
	trusttunnelMu.Lock()
	defer trusttunnelMu.Unlock()

	log.Infof("NewTrustTunnelClient() called")
	if trusttunnelClient != nil {
		_ = trusttunnelClient.Disconnect()
	}

	goConfig := C.GoString(config)
	goFD := int(fd)

	log.Infof("Creating TrustTunnel client with config len=%d and FD=%d", len(goConfig), goFD)

	trusttunnelClient = trusttunnel.NewTrustTunnelClient(goConfig, goFD)
	log.Infof("NewXrayClient() finished")
}
