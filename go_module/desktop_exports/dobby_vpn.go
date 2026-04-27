package main

import "C"
import (
	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/xray"
	"strings"
	"sync"
)

var vpnClient *core.CoreClient
var vpnMu sync.Mutex

var vpnLastError string
var vpnErrorMu sync.Mutex

//export GetVpnLastError
func GetVpnLastError() *C.char {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()
	if vpnLastError == "" {
		return nil
	}
	return C.CString(vpnLastError)
}

func setVpnLastError(err string) {
	vpnErrorMu.Lock()
	defer vpnErrorMu.Unlock()
	vpnLastError = err
	if err != "" {
		log.Infof("Vpn error set: %s", err)
	}
}

//export StartVpn
<<<<<<< HEAD:go_module/desktop_exports/dobby_vpn.go
func StartVpn(config *C.char, protocol *C.char) C.int {
=======
func StartVpn(config, protocol string) int32 {
	if !log.IsInitialized() {
		log.Errorf("Logger is not initialized")
		setVpnLastError("Logger is not initialized. Call InitLogger first.")
		return -1
	}
>>>>>>> c31f1510 (add error handling for desktop grpc):go_module/desktop_exports/api/protocols.go
	log.Infof("StartVpn")
	setVpnLastError("")
	str_config := C.GoString(config)
	str_protocol := strings.ToLower(C.GoString(protocol))

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

	switch str_protocol {
	case "xray":
		device, err = xray.NewXrayDevice(str_config)
	case "outline":
		device, err = outline.NewOutlineDevice(str_config)
	default:
		setVpnLastError("unsupported protocol: " + str_protocol)
		log.Infof("NewVpnClient() failed: unsupported protocol")
		return -1
	}

	if err != nil {
		log.Infof("Failed to create device for %s protocol: %v", str_protocol, err)
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

//export StopVpn
func StopVpn() {
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
