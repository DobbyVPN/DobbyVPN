//go:build ios

package cloak_outline

import (
	"fmt"
	"go_module/awg"
	"go_module/log"
	"strings"
)

var awgClient *awg.AwgClient

func AwgTurnOn(interfaceName string, tunFd int32, settings string) error {
	log.Infof("Create awg client")

	if awgClient != nil {
		log.Infof("Disconnecting previous client")
		err := awgClient.Disconnect()
		if err != nil {
			log.Infof("[WARNING] Failed to disconnect previous client")
		}
	}

	log.Infof("Config length=%d", len(settings))

	client, err := awg.NewAwgClient(strings.Clone(interfaceName), strings.Clone(settings), int(tunFd))
	if err != nil {
		log.Infof("Failed to create awg client: %v", err)
		return fmt.Errorf("failed to create awg client: %v", err)
	}

	awgClient = client

	log.Infof("Created awg client")

	log.Infof("Connecting awg client")
	err = awgClient.Connect()
	if err != nil {
		log.Infof("Failed to connect awg client: %v", err)
		return fmt.Errorf("failed to connect awg client: %v", err)
	}

	log.Infof("Connected awg client")
	return nil
}

func AwgTurnOff() {
	if awgClient == nil {
		log.Infof("Awg client is null")
		return
	}

	log.Infof("Disconnecting awg client")
	err := awgClient.Disconnect()
	if err != nil {
		log.Infof("Failed to disconnect awg client: %v", err)
	}
	awgClient = nil
}
