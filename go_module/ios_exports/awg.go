package cloak_outline

import (
	"go_module/awg"
	"go_module/log"
	"strings"
)

var awgClient *awg.AwgClient

func AwgTurnOn(settings string) int32 {
	log.Debugf(Category, "Create awg client")

	if awgClient != nil {
		log.Debugf(Category, "Disconnecting previous client")
		err := awgClient.Disconnect()
		if err != nil {
			log.Debugf(Category, "[WARNING] Failed to disconnect previous client")
		}
	}

	log.Debugf(Category, "Config length=%d", len(settings))

	client, err := awg.NewAwgClient(strings.Clone(settings))
	if err != nil {
		log.Debugf(Category, "Failed to create awg client: %v", err)
		return -1
	}

	awgClient = client

	log.Debugf(Category, "Created awg client")

	log.Debugf(Category, "Connecting awg client")
	err = awgClient.Connect()
	if err != nil {
		log.Debugf(Category, "Failed to connect awg client: %v", err)
		return -1
	}

	log.Debugf(Category, "Connected awg client")
	return 0
}

func AwgTurnOff() {
	if awgClient == nil {
		log.Debugf(Category, "Awg client is null")
		return
	}

	log.Debugf(Category, "Disconnecting awg client")
	err := awgClient.Disconnect()
	if err != nil {
		log.Debugf(Category, "Failed to disconnect awg client: %v", err)
	}
	awgClient = nil
}
