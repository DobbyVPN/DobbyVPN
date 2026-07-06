//go:build android

package dobbyvpn

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

	"golang.org/x/sys/unix"
)

var vpnClient *core.CoreClient
var lastError string

var errorMu sync.Mutex
var clientMu sync.Mutex

// ==========================================
// ERROR HANDLING & GUARDS
// ==========================================

func GetVpnLastError() string {
	errorMu.Lock()
	defer errorMu.Unlock()
	return lastError
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
	log.Debugf("kotlin_exports", "Error set: %s", err)
}

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			setLastError(msg)
			log.Debugf("kotlin_exports", "%s\n%s", msg, string(debug.Stack()))
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
		log.Debugf("kotlin_exports", "disconnectLocked(): client is already nil")
		return
	}

	_ = vpnClient.Disconnect()
	vpnClient = nil

	log.Debugf("kotlin_exports", "disconnectLocked(): finished")
}

func newProtocolDevice(config string, protocol string) (pkg.ProtocolDevice, error) {
	switch protocol {
	case "xray":
		return xray.NewXrayDevice(config)
	case "outline":
		return outline.NewOutlineDevice(config)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", protocol)
	}
}

func closeUnusedTunFD(fd int32) {
	if fd < 0 {
		return
	}
	if err := unix.Close(int(fd)); err != nil {
		log.Debugf("kotlin_exports", "NewVpnClient(): failed to close unused duplicated tun fd=%d: %v", fd, err)
		return
	}
	log.Debugf("kotlin_exports", "NewVpnClient(): closed unused duplicated tun fd=%d", fd)
}

func NewVpnClient(config string, protocol string, fd int32) {
	defer guardExport("NewVpnClient")()

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Debugf("kotlin_exports", "NewVpnClient() called")
	clearLastError()

	config = strings.Clone(config)
	protocol = strings.Clone(protocol)

	if vpnClient != nil {
		log.Debugf("kotlin_exports", "NewVpnClient(): existing client detected, switching protocol device")
		closeUnusedTunFD(fd)

		device, err := newProtocolDevice(config, protocol)
		if err != nil {
			setLastError(err.Error())
			log.Debugf("kotlin_exports", "NewVpnClient() failed to create %s device for switch: %v", protocol, err)
			return
		}
		if err := vpnClient.SwitchDevice(device); err != nil {
			setLastError(err.Error())
			log.Debugf("kotlin_exports", "NewVpnClient(): switch protocol device failed: %v", err)
			return
		}

		log.Debugf("kotlin_exports", "NewVpnClient(): existing client protocol device switched")
		return
	}

	log.Debugf("kotlin_exports", "NewVpnClient(): config.len=%d protocol=%s fd=%d", len(config), protocol, fd)

	tunFile := os.NewFile(uintptr(fd), "tun")
	if tunFile == nil {
		setLastError("failed to create tun file from fd")
		return
	}

	device, err := newProtocolDevice(config, protocol)
	if err != nil {
		setLastError(err.Error())
		log.Debugf("kotlin_exports", "NewVpnClient() failed to create %s device: %v", protocol, err)
		if closeErr := tunFile.Close(); closeErr != nil {
			log.Debugf("kotlin_exports", "NewVpnClient(): failed to close tun fd after device creation error: %v", closeErr)
		}
		return
	}

	log.Debugf("kotlin_exports", "NewVpnClient(): created device type=%T", device)

	vpnClient = core.NewClient(device, tunFile)

	log.Debugf("kotlin_exports", "NewVpnClient() finished successfully")
}

func VpnConnect() int32 {
	defer guardExport("VpnConnect")()

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Debugf("kotlin_exports", "VpnConnect() called")
	if pendingErr := GetVpnLastError(); pendingErr != "" {
		log.Debugf("kotlin_exports", "VpnConnect() aborted because NewVpnClient failed: %s", pendingErr)
		return -1
	}
	clearLastError()

	if vpnClient == nil {
		setLastError("client is nil")
		log.Debugf("kotlin_exports", "VpnConnect() failed: client is nil")
		return -1
	}

	if err := vpnClient.Connect(); err != nil {
		setLastError(err.Error())
		log.Debugf("kotlin_exports", "VpnConnect() failed: %v", err)
		return -1
	}

	log.Debugf("kotlin_exports", "VpnConnect() finished successfully")
	return 0
}

func VpnDisconnect() {
	defer guardExport("VpnDisconnect")()

	clientMu.Lock()
	defer clientMu.Unlock()

	log.Debugf("kotlin_exports", "VpnDisconnect() called")
	disconnectLocked()
	log.Debugf("kotlin_exports", "VpnDisconnect() finished")
}
