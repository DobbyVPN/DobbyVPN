package main

import (
	"go_client/awg"
	log "go_client/logger"
)

var awgClient *awg.AwgClient

func StartAwg(tunnel, config string) {
	log.Infof("Starting awg")

	if awgClient != nil {
		log.Infof("Disconnect existing awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	log.Infof("Create new awgClient")

	_awgClient, err := awg.NewAwgClient(tunnel, config)
	if err != nil {
		log.Infof("Failed to create awgClient: %v", err)
		return
	}
	awgClient = _awgClient

	log.Infof("Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.Infof("Failed to connect awgClient: %v", err)
	}
}

func StopAwg() {
	log.Infof("Stopping awg")

	if awgClient != nil {
		log.Infof("Disconnect awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	} else {
		log.Infof("awgClient is null")
	}
}
