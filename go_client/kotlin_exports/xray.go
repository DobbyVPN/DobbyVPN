package main

/*
#include <stdlib.h>
*/
import "C"
import (
	log "go_client/logger"
	"go_client/xray"
	"sync"
)

var xrayClient *xray.XrayClient
var xrayMu sync.Mutex

//export XrayConnect
func XrayConnect() C.int {
	xrayMu.Lock()
	defer xrayMu.Unlock()
	log.Infof("XrayConnect() called")

	if xrayClient == nil {
		log.Infof("Connect() failed: client is nil")
		return -1
	}
	err := xrayClient.Connect()
	if err != nil {
		log.Infof("Connect() failed: %v", err)
		return -1
	}
	return 0
}

//export XrayDisconnect
func XrayDisconnect() {
	xrayMu.Lock()
	defer xrayMu.Unlock()
	log.Infof("XrayDisconnect() called")

	if xrayClient != nil {
		_ = xrayClient.Disconnect()
	}
}

//export NewXrayClient
func NewXrayClient(config *C.char, fd C.int) {
	xrayMu.Lock()
	defer xrayMu.Unlock()

	log.Infof("NewXrayClient() called")
	// Ensure cleanup of previous instance
	if xrayClient != nil {
		_ = xrayClient.Disconnect()
	}

	goConfig := C.GoString(config)
	goFD := int(fd)

	log.Infof("Creating Xray client with config len=%d and FD=%d", len(goConfig), goFD)

	// Calls your existing client_mobile.go logic
	xrayClient = xray.NewXrayClient(goConfig, goFD)
	log.Infof("NewXrayClient() finished")
}
