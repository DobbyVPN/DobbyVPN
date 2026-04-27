package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"go_module/log"
	"go_module/outline"
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
	log.Debugf(Category, "Error set: %s", err)
}

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			setLastError(msg)
			log.Warnf(Category, "%s\n%s", msg, string(debug.Stack()))
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
	log.Debugf(Category, "NewOutlineClient() called")

	OutlineDisconnect()

	goConfig := C.GoString(config)
	goFD := int(fd)

	log.Debugf(Category, "Config length=%d", len(goConfig))

	tunFile := os.NewFile(uintptr(goFD), "tun")

	client = outline.NewClient(goConfig, tunFile)

	log.Infof(Category, "NewOutlineClient() finished")
}

//export OutlineConnect
func OutlineConnect() C.int {
	defer guardExport("OutlineConnect")()
	log.Debugf(Category, "OutlineConnect() called")

	clearLastError()

	if client == nil {
		setLastError("client is nil")
		log.Errorf(Category, "OutlineConnect() failed: client is nil")
		return -1
	}

	err := client.Connect()
	if err != nil {
		setLastError(err.Error())
		log.Errorf(Category, "OutlineConnect() failed: %v", err)
		return -1
	}

	log.Infof(Category, "OutlineConnect() finished successfully")
	return 0
}

//export OutlineDisconnect
func OutlineDisconnect() {
	defer guardExport("OutlineDisconnect")()
	log.Debugf(Category, "OutlineDisconnect() called")

	if client == nil {
		log.Errorf(Category, "OutlineDisconnect(): client is nil")
		return
	}

	client.Disconnect()
	client = nil

	log.Infof(Category, "OutlineDisconnect() finished")
}
