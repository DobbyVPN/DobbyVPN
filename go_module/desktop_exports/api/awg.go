//go:build !(android || ios)

package api

import (
	"go_module/awg"
	"go_module/desktop_exports/common"
	"go_module/log"
)

var awgClient *awg.AwgClient

func StartAwg(tunnel, config string) {
	log.Debugf(common.Category, "Starting awg")

	if awgClient != nil {
		log.Debugf(common.Category, "Disconnect existing awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Debugf(common.Category, "Failed to disconnect existing awgClient: %v", err)
			return
		}
	}

	log.Debugf(common.Category, "Create new awgClient")

	_awgClient, err := awg.NewAwgClient(config)
	if err != nil {
		log.Debugf(common.Category, "Failed to create awgClient: %v", err)
		return
	}
	awgClient = _awgClient

	log.Debugf(common.Category, "Connect awgClient")
	err = awgClient.Connect()
	if err != nil {
		log.Debugf(common.Category, "Failed to connect awgClient: %v", err)
	}
}

func StopAwg() {
	log.Debugf(common.Category, "Stopping awg")

	if awgClient != nil {
		log.Debugf(common.Category, "Disconnect awgClient")

		err := awgClient.Disconnect()
		if err != nil {
			log.Debugf(common.Category, "Failed to disconnect awgClient: %v", err)
		}
		awgClient = nil
	} else {
		log.Debugf(common.Category, "awgClient is null")
	}
}
