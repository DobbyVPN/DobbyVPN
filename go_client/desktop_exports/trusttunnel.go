package main

import "C"
import (
	log "go_client/logger"
	"go_client/trusttunnel"
	"sync"
)

var trusttunnelClient *trusttunnel.TrustTunnelClient
var trusttunnelMu sync.Mutex

//export StartTrustTunnel
func StartTrustTunnel(config *C.char) {
	log.Infof("StartTrustTunnel")
	str_config := C.GoString(config)

	log.Infof("Make lock")
	trusttunnelMu.Lock()
	defer trusttunnelMu.Unlock()
	log.Infof("locked")

	if trusttunnelClient != nil {
		log.Infof("Disconnect existing trusttunnel client")
		err := trusttunnelClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing trusttunnel client: %v", err)
			trusttunnelClient = nil
			return
		}
		trusttunnelClient = nil
	}

	trusttunnelClient = trusttunnel.NewTrustTunnelClient(str_config)
	log.Infof("Connect trusttunnel client")
	err := trusttunnelClient.Connect()
	if err != nil {
		log.Infof("Failed to connect trusttunnel client: %v", err)
		trusttunnelClient = nil
	}
}

//export StopTrustTunnel
func StopTrustTunnel() {
	trusttunnelMu.Lock()
	defer trusttunnelMu.Unlock()
	if trusttunnelClient != nil {
		log.Infof("Disconnect trusttunnel client")
		err := trusttunnelClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect trusttunnel client: %v", err)
		}
		trusttunnelClient = nil
	}
}
