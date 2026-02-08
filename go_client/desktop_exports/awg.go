package main

import (
	"log"

	"go_client/awg"
)

var awgClient *awg.AwgClient

func StartAwg(tunnel, config string) {
	log.Printf("Starting awg")

	if awgClient != nil {
		log.Printf("Disconnect existing awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Printf("Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	log.Printf("Create new awgClient")

	_awgClient, err := awg.NewAwgClient(tunnel, config)
	if err != nil {
		log.Printf("Failed to create awgClient: %v", err)
		return
	}
	awgClient = _awgClient

	log.Printf("Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.Printf("Failed to connect awgClient: %v", err)
	}
}

func StopAwg() {
	log.Printf("Stopping awg")

	if awgClient != nil {
		log.Printf("Disconnect awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Printf("Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	} else {
		log.Printf("awgClient is null")
	}
}
