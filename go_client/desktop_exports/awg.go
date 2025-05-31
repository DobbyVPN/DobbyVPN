package main

import "C"
import (
	log "github.com/sirupsen/logrus"
	"go_client/awg"
	"sync"
)

var awgClient *awg.AwgClient
var awgMu sync.Mutex

//export StartAwg
func StartAwg(key *C.char) {
	str_key := C.GoString(key)

	awgMu.Lock()
	defer awgMu.Unlock()

	if awgClient != nil {
		log.Infof("Disconnect existing awgClient")
		err := awgClient.Disconnect()
		if err != nil {
			log.Errorf("Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	_awgClient, err := awg.NewAwgClient(str_key)
	if err != nil {
		log.Errorf("Failed to create awgClient: %v", err)
		return
	}

	awgClient = _awgClient
	log.Infof("Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.Errorf("Failed to connect awgClient: %v", err)
	}
}

//export StopOutline
func StopAwg() {
	awgMu.Lock()
	defer awgMu.Unlock()
	if awgClient != nil {
		log.Infof("Disconnect awgClient")
		err := awgClient.Disconnect()
		if err != nil {
			log.Errorf("Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	}
}
