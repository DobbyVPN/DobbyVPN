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
		log.SimpleErrorf(ApiCategory, "Outline error set: %s", err)
	}
}

func StartOutline(key string) int32 {
	log.SimpleDebugf(ApiCategory, "StartOutline")
	setOutlineLastError("")
	str_key := key

	log.SimpleDebugf(ApiCategory, "Make lock")
	outlineMu.Lock()
	defer outlineMu.Unlock()
	log.SimpleDebugf(ApiCategory, "locked")

	if outlineClient != nil {
		log.SimpleDebugf(ApiCategory, "Disconnect existing outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.SimpleErrorf(ApiCategory, "Failed to disconnect existing outline client: %v", err)
			setOutlineLastError(err.Error())
			return -1
		}
	}

	outlineClient = outline.NewClient(str_key)
	log.SimpleDebugf(ApiCategory, "Connect outline client")
	err := outlineClient.Connect()
	if err != nil {
		log.SimpleErrorf(ApiCategory, "Failed to connect outline client: %v", err)
		setOutlineLastError(err.Error())
		return -1
	}
	log.SimpleInfof(ApiCategory, "Outline client connected successfully")
	return 0
}

func StopOutline() {
	outlineMu.Lock()
	defer outlineMu.Unlock()
	if outlineClient != nil {
		log.SimpleDebugf(ApiCategory, "Disconnect outline client")
		err := outlineClient.Disconnect()
		if err != nil {
			log.SimpleErrorf(ApiCategory, "Failed to disconnect outline client: %v", err)
		}
		outlineClient = nil
	}
	log.SimpleInfof(ApiCategory, "Outline client disconnected successfully")
}
