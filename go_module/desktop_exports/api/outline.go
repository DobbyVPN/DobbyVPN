//go:build !(android || ios)

package api

import (
	"go_module/core"
	"go_module/log"
	"go_module/outline"
)

var outlineClient *core.CoreClient

func StartOutline(config string) int32 {
	if outlineClient != nil {
		log.Infof("Disconnect existing outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing outline client: %v", err)
			return -1
		}
	}

	device, err := outline.NewOutlineDevice(config)
	if err != nil {
		log.Infof("Failed to create outline device: %v", err)
		return -1
	}

	outlineClient = core.NewClient(device)

	log.Infof("Connect outline client")
	err = outlineClient.Connect()
	if err != nil {
		log.Infof("Failed to connect outline client: %v", err)
		return -1
	}
	log.Infof("Outline client connected successfully")
	return 0
}

func StopOutline() {
	if outlineClient != nil {
		log.Infof("Disconnect outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect outline client: %v", err)
		}
		outlineClient = nil
	}
}

func GetOutlineLastError() string {
	return ""
}
