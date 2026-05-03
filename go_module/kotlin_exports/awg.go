//go:build android

package main

// extern int go_protect_socket(int fd);
import "C"

import (
	"go_module/awg"
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	"strings"

	"github.com/sirupsen/logrus"
)

func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}

	protected_dialer.MakeSocketProtected = func(fd uintptr) bool {
		return C.go_protect_socket(C.int(fd)) == 1
	}
}

var awgClient *awg.AwgClient

//export AwgTurnOn
func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	log.Debugf(Category, "Create awg client")

	if awgClient != nil {
		log.Debugf(Category, "Disconnecting previous client")
		err := awgClient.Disconnect()
		if err != nil {
			log.Debugf(Category, "[WARNING] Failed to disconnect previous client")
		}
	}

	log.Debugf(Category, "Config length=%d", len(settings))

	client, err := awg.NewAwgClient(strings.Clone(interfaceName), strings.Clone(settings), int(tunFd))
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

//export AwgTurnOff
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

//export AwgGetSocketV4
func AwgGetSocketV4() int32 {
	if awgClient == nil {
		log.Debugf(Category, "Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV4())
}

//export AwgGetSocketV6
func AwgGetSocketV6() int32 {
	if awgClient == nil {
		log.Debugf(Category, "Awg client is null")
		return -1
	}

	return int32(awgClient.App.TunnelData.GetSocketV6())
}
