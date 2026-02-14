package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	log "go_client/logger"
	"go_client/outline"
	"os"
	"runtime/debug"
	"sync"
)

var client *outline.OutlineClient
var lastError string
var errorMu sync.Mutex

//export GetLastError
func GetLastError() *C.char {
	errorMu.Lock()
	defer errorMu.Unlock()
	if lastError == "" {
		return nil
	}
	return C.CString(lastError)
}

func clearLastError() {
	errorMu.Lock()
	defer errorMu.Unlock()
	lastError = ""
}

func setLastError(err string) {
	errorMu.Lock()
	defer errorMu.Unlock()
	lastError = err
	log.Infof("Error set: %s", err)
}

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			setLastError(msg)
			log.Infof("%s\n%s", msg, string(debug.Stack()))
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return "non-string panic"
	}
}

//export NewOutlineClient
func NewOutlineClient(config *C.char, fd C.int) {
	defer guardExport("NewOutlineClient")()
	log.Infof("NewOutlineClient() called")

	OutlineDisconnect()

	goConfig := C.GoString(config)
	goFD := int(fd)

	log.Infof("Config length=%d", len(goConfig))

	tunFile := os.NewFile(uintptr(goFD), "tun")

	client = outline.NewClient(goConfig, tunFile)

	log.Infof("NewOutlineClient() finished")
}

//export OutlineConnect
func OutlineConnect() C.int {
	defer guardExport("OutlineConnect")()
	log.Infof("OutlineConnect() called")

	clearLastError()

	if client == nil {
		setLastError("client is nil")
		log.Infof("OutlineConnect() failed: client is nil")
		return -1
	}

	err := client.Connect()
	if err != nil {
		setLastError(err.Error())
		log.Infof("OutlineConnect() failed: %v", err)
		return -1
	}

	log.Infof("OutlineConnect() finished successfully")
	return 0
}

//export OutlineDisconnect
func OutlineDisconnect() {
	defer guardExport("OutlineDisconnect")()
	log.Infof("OutlineDisconnect() called")

	if client == nil {
		log.Infof("OutlineDisconnect(): client is nil")
		return
	}

	client.Disconnect()
	client = nil

	log.Infof("OutlineDisconnect() finished")
}
