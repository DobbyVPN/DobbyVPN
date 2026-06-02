//go:build !(android || ios)

package api

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

func getVpnLastError() string {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()

	return vpnLastError
}

func setVpnLastError(err string) {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()
	vpnLastError = err
	if err != "" {
		log.Infof("Vpn error set: %s", err)
	}
}

func startVpn(config, protocol string) int32 {
	if !log.IsInitialized() {
		log.Errorf("Logger is not initialized")
		setVpnLastError("Logger is not initialized. Call InitLogger first.")
		return -1
	}
	log.Infof("StartVpn")
	setVpnLastError("")

	log.Infof("Make lock")
	vpnMu.Lock()
	defer vpnMu.Unlock()
	log.Infof("locked")

	if vpnClient != nil {
		log.Infof("Disconnect existing vpn client")
		err := vpnClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect existing vpn client: %v", err)
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
		log.Infof("NewVpnClient() failed: unsupported protocol")
		return -1
	}

	if err != nil {
		log.Infof("Failed to create device for %s protocol: %v", protocol, err)
		setVpnLastError(err.Error())
		return -1
	}

	vpnClient = core.NewClient(device)

	log.Infof("Connect vpn client")
	err = vpnClient.Connect()
	if err != nil {
		log.Infof("Failed to connect vpn client: %v", err)
		setVpnLastError(err.Error())
		return -1
	}
	log.Infof("Vpn client connected successfully")
	return 0
}

func stopVpn() {
	vpnMu.Lock()
	defer vpnMu.Unlock()
	if vpnClient != nil {
		log.Infof("Disconnect vpn client")
		err := vpnClient.Disconnect()
		if err != nil {
			log.Infof("Failed to disconnect vpn client: %v", err)
		}
		vpnClient = nil
	}
}
