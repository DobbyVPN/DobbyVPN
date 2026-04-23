package api

import (
	"go_module/awg"
	"go_module/log"
)

var awgClient *awg.AwgClient

func StartAwg(tunnel, config string) {
	log.SimpleDebugf(ApiCategory, "Starting awg")

	if awgClient != nil {
		log.SimpleDebugf(ApiCategory, "Disconnect existing awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.SimpleErrorf(ApiCategory, "Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	log.SimpleDebugf(ApiCategory, "Create new awgClient")

	_awgClient, err := awg.NewAwgClient(config)
	if err != nil {
		log.SimpleErrorf(ApiCategory, "Failed to create awgClient: %v", err)
		return
	}
	awgClient = _awgClient

	log.SimpleDebugf(ApiCategory, "Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.SimpleErrorf(ApiCategory, "Failed to connect awgClient: %v", err)
	}
	log.SimpleInfof(ApiCategory, "AmneziaWG client connected successfully")
}

func StopAwg() {
	log.SimpleDebugf(ApiCategory, "Stopping awg")

	if awgClient != nil {
		log.SimpleDebugf(ApiCategory, "Disconnect awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.SimpleErrorf(ApiCategory, "Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	} else {
		log.SimpleDebugf(ApiCategory, "awgClient is null")
	}

	log.SimpleInfof(ApiCategory, "AmneziaWG client disconnected successfully")
}
