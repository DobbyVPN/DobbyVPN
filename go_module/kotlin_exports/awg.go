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
	log.Debugf("kotlin_exports", "Create awg client")

	if awgClient != nil {
		log.Debugf("kotlin_exports", "Disconnecting previous client")
		err := awgClient.Disconnect()
		if err != nil {
			log.Debugf("kotlin_exports", "[WARNING] Failed to disconnect previous client")
		}
	}

	log.Debugf("kotlin_exports", "Config length=%d", len(settings))

	client, err := awg.NewAwgClient(strings.Clone(interfaceName), strings.Clone(settings), int(tunFd))
	if err != nil {
		log.Debugf("kotlin_exports", "Failed to create awg client: %v", err)
		return -1
	}

	awgClient = client

	log.Debugf("kotlin_exports", "Created awg client")

	log.Debugf("kotlin_exports", "Connecting awg client")
	err = awgClient.Connect()
	if err != nil {
		log.Debugf("kotlin_exports", "Failed to connect awg client: %v", err)
		return -1
	}

	log.Debugf("kotlin_exports", "Connected awg client")
	return 0
}

func AwgTurnOff() {
	if awgClient == nil {
		log.Debugf("kotlin_exports", "Awg client is null")
		return
	}

	log.Debugf("kotlin_exports", "Disconnecting awg client")
	err := awgClient.Disconnect()
	if err != nil {
		log.Debugf("kotlin_exports", "Failed to disconnect awg client: %v", err)
	}
	awgClient = nil
}

func AwgGetSocketV4() int32 {
	if awgClient == nil {
		log.Debugf("kotlin_exports", "Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV4())
}

func AwgGetSocketV6() int32 {
	if awgClient == nil {
		log.Debugf("kotlin_exports", "Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV6())
}
