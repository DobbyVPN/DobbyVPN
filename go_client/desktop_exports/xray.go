package main

import "C"
import (
	log "go_client/logger"
	"go_client/xray"
	"sync"
)

var xrayClient *xray.XrayClient
var xrayMu sync.Mutex

//export StartXray
func StartXray(config *C.char) {
	log.Infof("StartXray")
	str_config := C.GoString(config)

	log.Infof("Make lock")
	xrayMu.Lock()
	defer xrayMu.Unlock()
	log.Infof("locked")

	if xrayClient != nil {
		log.Infof("Disconnect existing xray client")
		err := xrayClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing xray client: %v", err)
			return
		}
	}

	xrayClient = xray.NewXrayClient(str_config)
	log.Infof("Connect xray client")
	err := xrayClient.Connect()
	if err != nil {
		log.Infof("Failed to connect xray client: %v", err)
	}
}

//export StopXray
func StopXray() {
	xrayMu.Lock()
	defer xrayMu.Unlock()
	if xrayClient != nil {
		log.Infof("Disconnect xray client")
		err := xrayClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect xray client: %v", err)
		}
		xrayClient = nil
	}
}
