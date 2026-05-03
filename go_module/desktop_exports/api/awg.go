//go:build !(android || ios)

package api

import (
	"go_module/awg"
	"go_module/log"
)

var awgClient *awg.AwgClient

func StartAwg(tunnel, config string) {
	log.Debugf(Category, "Starting awg")

	if awgClient != nil {
		log.Debugf(Category, "Disconnect existing awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Errorf(Category, "Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	log.Debugf(Category, "Create new awgClient")

	_awgClient, err := awg.NewAwgClient(config)
	if err != nil {
		log.Errorf(Category, "Failed to create awgClient: %v", err)
		return
	}
	awgClient = _awgClient

	log.Debugf(Category, "Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.Errorf(Category, "Failed to connect awgClient: %v", err)
	}
	log.Infof(Category, "AmneziaWG client connected successfully")
}

func StopAwg() {
	log.Debugf(Category, "Stopping awg")

	if awgClient != nil {
		log.Debugf(Category, "Disconnect awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Errorf(Category, "Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	} else {
		log.Debugf(Category, "awgClient is null")
	}

	log.Infof(Category, "AmneziaWG client disconnected successfully")
}
