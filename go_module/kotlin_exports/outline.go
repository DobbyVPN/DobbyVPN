//go:build android

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
)

// client is the dedicated Outline mobile client instance.
// Error handling helpers (lastError, clearLastError, setLastError, guardExport,
// unsafeToString) are shared with the core-based VPN exports in dobby_vpn.go.

var client *outline.OutlineClient

//export GetLastError
func GetLastError() *C.char {
	// Reuse the shared error plumbing from dobby_vpn.go so that
	// OutlineGo.getLastError() and GoBackend.getLastError() see the same state.
	return GetVpnLastError()
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
