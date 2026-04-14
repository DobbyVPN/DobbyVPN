package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/xray"
)

var vpnClient *core.CoreClient
var lastError string
var errorMu sync.Mutex

// ==========================================
// ERROR HANDLING & GUARDS
// ==========================================

//export GetVpnLastError
func GetVpnLastError() *C.char {
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

// ==========================================
// VPN LIFECYCLE CONTROLS
// ==========================================

//export NewVpnClient
func NewVpnClient(config *C.char, protocol *C.char, fd C.int) {
	defer guardExport("NewVpnClient")()
	log.Infof("NewVpnClient() called")

	// Ensure any zombie connection is completely cleaned up
	VpnDisconnect()

	goConfig := C.GoString(config)
	goProtocol := strings.ToLower(C.GoString(protocol))
	goFD := int(fd)

	log.Infof("Config length=%d, protocol=%s", len(goConfig), goProtocol)

	tunFile := os.NewFile(uintptr(goFD), "tun")

	var device pkg.ProtocolDevice
	var err error

	// Factory: Create the protocol-specific device
	switch goProtocol {
	case "xray":
		device, err = xray.NewXrayDevice(goConfig)
	// case "outline": TODO
	// 	device, err = outline.NewOutlineDevice(goConfig)
	default:
		setLastError("unsupported protocol: " + goProtocol)
		log.Infof("NewVpnClient() failed: unsupported protocol")
		return
	}

	if err != nil {
		setLastError(err.Error())
		log.Infof("NewVpnClient() failed to create %s device: %v", goProtocol, err)
		return
	}

	// Inject the protocol device into the universal mobile client
	vpnClient = core.NewClient(device, tunFile)

	log.Infof("NewVpnClient() finished successfully")
}

//export VpnConnect
func VpnConnect() C.int {
	defer guardExport("VpnConnect")()
	log.Infof("VpnConnect() called")

	clearLastError()

	if vpnClient == nil {
		setLastError("client is nil")
		log.Infof("VpnConnect() failed: client is nil")
		return -1
	}

	err := vpnClient.Connect()
	if err != nil {
		setLastError(err.Error())
		log.Infof("VpnConnect() failed: %v", err)
		return -1
	}

	log.Infof("VpnConnect() finished successfully")
	return 0
}

//export VpnDisconnect
func VpnDisconnect() {
	defer guardExport("VpnDisconnect")()
	log.Infof("VpnDisconnect() called")

	if vpnClient == nil {
		log.Infof("VpnDisconnect(): client is already nil")
		return
	}

	_ = vpnClient.Disconnect()

	// FIX: Annihilate the client pointer to prevent Xray zombie processes
	vpnClient = nil

	log.Infof("VpnDisconnect() finished")
}
