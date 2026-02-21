package main

import (
	"go_client/outline"
	"sync"

	log "go_client/logger"
)

var outlineClient *outline.OutlineClient
var outlineMu sync.Mutex

var outlineLastError string
var outlineErrorMu sync.Mutex

func GetOutlineLastError() string {
	outlineErrorMu.Lock()
	defer outlineErrorMu.Unlock()

	return outlineLastError
}

func setOutlineLastError(err string) {
	outlineErrorMu.Lock()
	defer outlineErrorMu.Unlock()
	outlineLastError = err
	if err != "" {
		log.Infof("Outline error set: %s", err)
	}
}

func StartOutline(key string) int32 {
	log.Infof("StartOutline")
	setOutlineLastError("")
	str_key := key

	log.Infof("Make lock")
	outlineMu.Lock()
	defer outlineMu.Unlock()
	log.Infof("locked")

	if outlineClient != nil {
		log.Infof("Disconnect existing outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing outline client: %v", err)
			setOutlineLastError(err.Error())
			return -1
		}
	}

	outlineClient = outline.NewClient(str_key)
	log.Infof("Connect outline client")
	err := outlineClient.Connect()
	if err != nil {
		log.Infof("Failed to connect outline client: %v", err)
		setOutlineLastError(err.Error())
		return -1
	}
	log.Infof("Outline client connected successfully")
	return 0
}

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
