package main

import (
	"go_client/outline"
	"sync"

	log "go_client/logger"
)

var outlineClient *outline.OutlineClient
var outlineMu sync.Mutex

func StartOutline(str_key string) {
	log.Infof("StartOutline")

	log.Infof("Make lock")
	outlineMu.Lock()
	defer outlineMu.Unlock()
	log.Infof("locked")

	if outlineClient != nil {
		log.Infof("Disconnect existing outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing outline client: %v", err)
			return
		}
	}

	outlineClient = outline.NewClient(str_key)
	log.Infof("Connect outline client")
	err := outlineClient.Connect()
	if err != nil {
		log.Infof("Failed to connect outline client: %v", err)
	}
}

//export StopOutline
func StopOutline() {
	outlineMu.Lock()
	defer outlineMu.Unlock()
	if outlineClient != nil {
		log.Infof("Disconnect outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect outline client: %v", err)
		}
		outlineClient = nil
	}
}
