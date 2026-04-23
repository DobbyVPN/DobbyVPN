//go:build android

package main

// extern int go_protect_socket(int fd);
import "C"

import (
	"go_module/awg"
	"go_module/log"
	"go_module/tunnel/protected_dialer"

	"github.com/sirupsen/logrus"
)

func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}

	protected_dialer.MakeSocketProtected = func(fd uintptr) {
		C.go_protect_socket(C.int(fd))
	}
}

var awgClient *awg.AwgClient

//export AwgTurnOn
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

	client, err := awg.NewAwgClient(interfaceName, settings, int(tunFd))
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

//export AwgTurnOff
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
}

//export AwgGetSocketV4
func AwgGetSocketV4() int32 {
	if awgClient == nil {
		log.Infof("Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV4())
}

//export AwgGetSocketV6
func AwgGetSocketV6() int32 {
	if awgClient == nil {
		log.Infof("Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV6())
}
