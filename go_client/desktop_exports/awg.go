package main

import (
	"go_client/awg"
	"sync"

	log "github.com/sirupsen/logrus"
)

var awgClient *awg.AwgClient
var awgMu sync.Mutex

func StartAwg(tunnel, config string) {
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

	_awgClient, err := awg.NewAwgClient(tunnel, config)
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
