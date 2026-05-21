//go:build android

package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"fmt"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/xray"
)

var vpnClient *core.CoreClient
var lastError string

var errorMu sync.Mutex
var clientMu sync.Mutex

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
	return fmt.Sprint(v)
}

// ==========================================
// VPN LIFECYCLE CONTROLS
// ==========================================

func disconnectLocked() {
	if vpnClient == nil {
		log.Infof("disconnectLocked(): client is already nil")
		return
	}

	_ = vpnClient.Disconnect()
	vpnClient = nil

	log.Infof("disconnectLocked(): finished")
}

//export NewVpnClient
func NewVpnClient(config string, protocol string, fd int32) {
	defer guardExport("NewVpnClient")()

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Infof("NewVpnClient() called")
	clearLastError()

	config = strings.Clone(config)
	protocol = strings.Clone(protocol)

	disconnectLocked()

	log.Infof("NewVpnClient(): config.len=%d protocol=%s fd=%d", len(config), protocol, fd)

	tunFile := os.NewFile(uintptr(fd), "tun")
	if tunFile == nil {
		setLastError("failed to create tun file from fd")
		return
	}

	var device pkg.ProtocolDevice
	var err error

	switch protocol {
	case "xray":
		device, err = xray.NewXrayDevice(config)
	case "outline":
		device, err = outline.NewOutlineDevice(config)
	default:
		setLastError("unsupported protocol: " + protocol)
		log.Infof("NewVpnClient() failed: unsupported protocol=%s", protocol)
		return
	}

	if err != nil {
		setLastError(err.Error())
		log.Infof("NewVpnClient() failed to create %s device: %v", protocol, err)
		return
	}

	log.Infof("NewVpnClient(): created device type=%T", device)

	vpnClient = core.NewClient(device, tunFile)

	log.Infof("NewVpnClient() finished successfully")
}

//export VpnConnect
func VpnConnect() int32 {
	defer guardExport("VpnConnect")()

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Infof("VpnConnect() called")
	clearLastError()

	if vpnClient == nil {
		setLastError("client is nil")
		log.Infof("VpnConnect() failed: client is nil")
		return -1
	}

	if err := vpnClient.Connect(); err != nil {
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

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Infof("VpnDisconnect() called")
	disconnectLocked()
	log.Infof("VpnDisconnect() finished")
}
