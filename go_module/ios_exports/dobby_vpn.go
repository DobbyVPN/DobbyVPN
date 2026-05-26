package cloak_outline

import (
	"fmt"
	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/xray"
	"runtime/debug"
)

var client *core.CoreClient

func guardExportErr(fnName string, errp *error) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Debugf(Category, "%s\n%s", msg, string(debug.Stack()))
			if errp != nil {
				*errp = fmt.Errorf("%s", msg)
			}
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func NewVpnClient(transportConfig string, protocol string, tunnelFD int, mtu int) (err error) {
	defer guardExportErr("NewVpnClient", &err)()
	log.Debugf(Category, "NewVpnClient() called protocol=%s fd=%d mtu=%d", protocol, tunnelFD, mtu)
	logNativeBuildInfo("NewVpnClient()")

	if client != nil {
		log.Debugf(Category, "NewVpnClient(): existing client detected, disconnecting previous client first status=%s", client.Status())
		if err := VpnDisconnect(); err != nil {
			log.Debugf(Category, "NewVpnClient(): previous client disconnect failed: %v", err)
			return fmt.Errorf("NewVpnClient(): disconnect failed: %w", err)
		}
		log.Debugf(Category, "NewVpnClient(): previous client disconnected")
	}

	if tunnelFD < 0 {
		log.Debugf(Category, "NewVpnClient(): invalid tunnel fd %d", tunnelFD)
		return fmt.Errorf("NewVpnClient(): invalid tunnel fd %d", tunnelFD)
	}

	log.Debugf(Category, "Using tunnel fd=%d mtu=%d", tunnelFD, mtu)
	log.Debugf(Category, "Config length=%d", len(transportConfig))

	var device pkg.ProtocolDevice

	// Factory: Create the protocol-specific device
	switch protocol {
	case "xray":
		log.Debugf(Category, "NewVpnClient(): creating xray device")
		device, err = xray.NewXrayDevice(transportConfig)
	case "outline":
		log.Debugf(Category, "NewVpnClient(): creating outline device preferTCPDNSForWebSocket=true disableNonDNSUDP=true")
		device, err = outline.NewOutlineDeviceWithOptions(transportConfig, outline.DeviceOptions{
			PreferTCPDNSForWebSocket: true,
			DisableNonDNSUDP:         true,
		})
	default:
		log.Debugf(Category, "NewVpnClient() failed: unsupported protocol")
		return fmt.Errorf("unsupported protocol: " + protocol)
	}

	if err != nil {
		log.Debugf(Category, "NewVpnClient() failed to create device: %v", err)
		return fmt.Errorf("failed to create %s device: %w", protocol, err)
	}

	client = core.NewClient(device, tunnelFD, mtu)
	log.Debugf(Category, "NewVpnClient(): core client allocated status=%s", client.Status())

	log.Debugf(Category, "NewVpnClient() finished")
	return nil
}

func VpnConnect() (err error) {
	defer guardExportErr("VpnConnect", &err)()
	log.Debugf(Category, "VpnConnect() called")

	if client == nil {
		log.Debugf(Category, "VpnConnect() failed: client is nil")
		return fmt.Errorf("VpnConnect(): client is nil")
	}

	log.Debugf(Category, "VpnConnect(): before Connect status=%s", client.Status())
	if err := client.Connect(); err != nil {
		log.Debugf(Category, "VpnConnect() failed: %v", err)
		return fmt.Errorf("VpnConnect(): %w", err)
	}

	log.Debugf(Category, "VpnConnect() finished successfully status=%s", client.Status())
	return nil
}

func VpnDisconnect() (err error) {
	defer guardExportErr("VpnDisconnect", &err)()
	log.Debugf(Category, "VpnDisconnect() called")

	if client == nil {
		log.Debugf(Category, "VpnDisconnect(): client already nil")
		return nil
	}

	log.Debugf(Category, "VpnDisconnect(): before Disconnect status=%s", client.Status())
	if err := client.Disconnect(); err != nil {
		log.Debugf(Category, "VpnDisconnect(): client disconnect failed: %v", err)
		return err
	}
	client = nil

	log.Debugf(Category, "VpnDisconnect() finished")
	return nil
}

func VpnStatus() (status string, err error) {
	defer guardExportErr("VpnStatus", &err)()

	if client == nil {
		log.Debugf(Category, "VpnStatus: client=nil")
		return "client=false engineStarted=false fd=-1 deviceNil=true reason=client_nil", nil
	}

	status = client.Status()
	log.Debugf(Category, "VpnStatus: %s", status)
	return status, nil
}
