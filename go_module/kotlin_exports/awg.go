//go:build android

package dobbyvpn

import (
	"go_module/awg"
	"go_module/log"
	"strings"

	"github.com/sirupsen/logrus"
)

func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}
}

var awgClient *awg.AwgClient

func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
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
		return -1
	}

	awgClient = client

	log.Infof("Created awg client")

	log.Infof("Connecting awg client")
	err = awgClient.Connect()
	if err != nil {
		log.Infof("Failed to connect awg client: %v", err)
		return -1
	}

	log.Infof("Connected awg client")
	return 0
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

func AwgGetSocketV4() int32 {
	if awgClient == nil {
		log.Infof("Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV4())
}

func AwgGetSocketV6() int32 {
	if awgClient == nil {
		log.Infof("Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV6())
}
