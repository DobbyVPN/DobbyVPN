//go:build android

package main

/*
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"runtime/debug"
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
func NewVpnClient(config string, protocol string, fd int32, mtu int32) {
	defer guardExport("NewVpnClient")()
	log.Infof("NewVpnClient() called protocol=%s fd=%d mtu=%d", protocol, fd, mtu)

	// Ensure any zombie connection is completely cleaned up
	VpnDisconnect()

	log.Infof("Config length=%d", len(config))

	var device pkg.ProtocolDevice
	var err error

	// Factory: Create the protocol-specific device
	switch protocol {
	case "xray":
		device, err = xray.NewXrayDevice(config)
	case "outline":
		device, err = outline.NewOutlineDeviceWithOptions(config, outline.DeviceOptions{
			PreferTCPDNSForWebSocket: true,
		})
	default:
		setLastError("unsupported protocol: " + protocol)
		log.Infof("NewVpnClient() failed: unsupported protocol")
		return
	}

	if err != nil {
		setLastError(err.Error())
		log.Infof("NewVpnClient() failed to create %s device: %v", protocol, err)
		return
	}

	// Inject the protocol device into the universal mobile client
	vpnClient = core.NewClient(device, int(fd), int(mtu))

	log.Infof("NewVpnClient() finished successfully")
}

//export VpnConnect
func VpnConnect() int32 {
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

	vpnClient = nil
	log.Infof("VpnDisconnect() finished")
}
