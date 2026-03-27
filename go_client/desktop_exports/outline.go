package main

import "C"
import (
	log "go_client/logger"
	"go_client/outline"
	"sync"
)

var outlineClient *outline.OutlineClient
var outlineMu sync.Mutex

var outlineLastError string
var outlineErrorMu sync.Mutex

//export GetOutlineLastError
func GetOutlineLastError() *C.char {
	outlineErrorMu.Lock()
	defer outlineErrorMu.Unlock()
	if outlineLastError == "" {
		return nil
	}
	return C.CString(outlineLastError)
}

func setOutlineLastError(err string) {
	outlineErrorMu.Lock()
	defer outlineErrorMu.Unlock()
	outlineLastError = err
	if err != "" {
		log.Infof("Outline error set: %s", err)
	}
}

//export StartOutline
func StartOutline(key *C.char) C.int {
	log.Infof("StartOutline")
	setOutlineLastError("")
	str_key := C.GoString(key)

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
