package api

import "C"
import (
	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/xray"
	"sync"
)

var vpnClient *core.CoreClient
var vpnMu sync.Mutex

var vpnLastError string
var vpnErrorMu sync.Mutex

//export GetVpnLastError
func GetVpnLastError() string {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()

	return vpnLastError
}

func setVpnLastError(err string) {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()
	vpnLastError = err
	if err != "" {
		log.Debugf(Category, "Vpn error set: %s", err)
	}
}

//export StartVpn
func StartVpn(config, protocol string) int32 {
	if !log.IsInitialized() {
		log.Errorf(Category, "Logger is not initialized")
		setVpnLastError("Logger is not initialized. Call InitLogger first.")
		return -1
	}
	log.Debugf(Category, "StartVpn")
	setVpnLastError("")

	log.Debugf(Category, "Make lock")
	vpnMu.Lock()
	defer vpnMu.Unlock()
	log.Debugf(Category, "locked")

	if vpnClient != nil {
		log.Debugf(Category, "Disconnect existing vpn client")
		err := vpnClient.Disconnect()
		if err != nil {
			log.Debugf(Category, "Failed to disconnect existing vpn client: %v", err)
			setVpnLastError(err.Error())
			return -1
		}
	}

	var device pkg.ProtocolDevice
	var err error

	switch protocol {
	case "xray":
		device, err = xray.NewXrayDevice(config)
	case "outline":
		device, err = outline.NewOutlineDevice(config)
	default:
		setVpnLastError("unsupported protocol: " + protocol)
		log.Debugf(Category, "NewVpnClient() failed: unsupported protocol")
		return -1
	}

	if err != nil {
		log.Debugf(Category, "Failed to create device for %s protocol: %v", protocol, err)
		setVpnLastError(err.Error())
		return -1
	}

	vpnClient = core.NewClient(device)

	log.Debugf(Category, "Connect vpn client")
	err = vpnClient.Connect()
	if err != nil {
		log.Debugf(Category, "Failed to connect vpn client: %v", err)
		setVpnLastError(err.Error())
		return -1
	}
	log.Debugf(Category, "Vpn client connected successfully")
	return 0
}

//export StopVpn
func StopVpn() {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	if vpnClient != nil {
		log.Debugf(Category, "Disconnect vpn client")
		err := vpnClient.Disconnect()
		if err != nil {
			log.Debugf(Category, "Failed to disconnect vpn client: %v", err)
		}
		vpnClient = nil
	}
}
