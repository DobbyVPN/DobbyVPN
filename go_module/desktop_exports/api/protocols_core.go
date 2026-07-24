//go:build !(android || ios)

package api

import (
	"go_module/core"
	apiCommon "go_module/desktop_exports/common"
	"go_module/log"
	"go_module/vpnmanager"
)

var (
	vpnHolder  = vpnmanager.NewClientHolder(apiCommon.Category)
	vpnLastErr = vpnmanager.NewLastError(apiCommon.Category)
)

func getVpnLastError() string {
	return vpnLastErr.Get()
}

func setVpnLastError(err string) {
	vpnLastErr.Set(err)
}

func startVpn(config, protocol string) int32 {
	if !log.IsInitialized() {
		log.Errorf(apiCommon.Category, "Logger is not initialized")
		setVpnLastError("Logger is not initialized. Call InitLogger first.")
		return -1
	}
	log.Debugf(apiCommon.Category, "StartVpn")
	setVpnLastError("")

	log.Debugf(apiCommon.Category, "Make lock")
	vpnHolder.Lock()
	defer vpnHolder.Unlock()
	log.Debugf(apiCommon.Category, "locked")

	device, switched, err := vpnHolder.SwitchOrPrepareDevice(config, protocol)
	if err != nil {
		log.Debugf(apiCommon.Category, "Failed to create device for %s protocol: %v", protocol, err)
		setVpnLastError(err.Error())
		return -1
	}

	if switched {
		log.Debugf(apiCommon.Category, "Vpn client protocol hot-switch completed successfully")
		return 0
	}

	vpnHolder.SetClient(core.NewClient(device))

	log.Debugf(apiCommon.Category, "Connect vpn client")
	err = vpnHolder.Connect()
	if err != nil {
		log.Debugf(apiCommon.Category, "Failed to connect vpn client: %v", err)
		if disconnectErr := vpnHolder.DisconnectAndClear(); disconnectErr != nil {
			log.Debugf(apiCommon.Category, "Failed to clean up vpn client after connect error: %v", disconnectErr)
		}
		setVpnLastError(err.Error())
		return -1
	}
	log.Debugf(apiCommon.Category, "Vpn client connected successfully")
	return 0
}

func stopVpn() {
	vpnHolder.Lock()
	defer vpnHolder.Unlock()
	if vpnHolder.Client() != nil {
		log.Debugf(apiCommon.Category, "Disconnect vpn client")
		if err := vpnHolder.DisconnectAndClear(); err != nil {
			log.Debugf(apiCommon.Category, "Failed to disconnect vpn client: %v", err)
		}
	}
}

func GetVpnLastError() string {
	return getVpnLastError()
}

func StartVpn(config, protocol string) int32 {
	return startVpn(config, protocol)
}

func StopVpn() {
	stopVpn()
}
