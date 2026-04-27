package api

import (
	"go_module/log"
	"go_module/outline"
	"sync"
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
		log.Errorf(Category, "Outline error set: %s", err)
	}
}

func StartOutline(key string) int32 {
	log.Debugf(Category, "StartOutline")
	setOutlineLastError("")
	str_key := key

	log.Debugf(Category, "Make lock")
	outlineMu.Lock()
	defer outlineMu.Unlock()
	log.Debugf(Category, "locked")

	if outlineClient != nil {
		log.Debugf(Category, "Disconnect existing outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Errorf(Category, "Failed to disconnect existing outline client: %v", err)
			setOutlineLastError(err.Error())
			return -1
		}
	}

	outlineClient = outline.NewClient(str_key)
	log.Debugf(Category, "Connect outline client")
	err := outlineClient.Connect()
	if err != nil {
		log.Errorf(Category, "Failed to connect outline client: %v", err)
		setOutlineLastError(err.Error())
		return -1
	}
	log.Infof(Category, "Outline client connected successfully")
	return 0
}

func StopOutline() {
	outlineMu.Lock()
	defer outlineMu.Unlock()
	if outlineClient != nil {
		log.Debugf(Category, "Disconnect outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.Errorf(Category, "Failed to disconnect outline client: %v", err)
		}
		outlineClient = nil
	}
	log.Infof(Category, "Outline client disconnected successfully")
}
