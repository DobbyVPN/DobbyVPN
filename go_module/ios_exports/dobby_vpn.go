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
			log.Infof("%s\n%s", msg, string(debug.Stack()))
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
	log.Infof("NewVpnClient() called protocol=%s fd=%d mtu=%d", protocol, tunnelFD, mtu)

	if client != nil {
		log.Infof("NewVpnClient(): existing client detected, disconnecting previous client first status=%s", client.Status())
		if err := VpnDisconnect(); err != nil {
			log.Infof("NewVpnClient(): previous client disconnect failed: %v", err)
			return fmt.Errorf("NewVpnClient(): disconnect failed: %w", err)
		}
		log.Infof("NewVpnClient(): previous client disconnected")
	}

	if tunnelFD < 0 {
		log.Infof("NewVpnClient(): invalid tunnel fd %d", tunnelFD)
		return fmt.Errorf("NewVpnClient(): invalid tunnel fd %d", tunnelFD)
	}

	log.Infof("Using tunnel fd=%d mtu=%d", tunnelFD, mtu)
	log.Infof("Config length=%d", len(transportConfig))

	var device pkg.ProtocolDevice

	// Factory: Create the protocol-specific device
	switch protocol {
	case "xray":
		log.Infof("NewVpnClient(): creating xray device")
		device, err = xray.NewXrayDevice(transportConfig)
	case "outline":
		log.Infof("NewVpnClient(): creating outline device preferTCPDNSForWebSocket=true disableNonDNSUDP=true")
		device, err = outline.NewOutlineDeviceWithOptions(transportConfig, outline.DeviceOptions{
			PreferTCPDNSForWebSocket: true,
			DisableNonDNSUDP:        true,
		})
	default:
		log.Infof("NewVpnClient() failed: unsupported protocol")
		return fmt.Errorf("unsupported protocol: " + protocol)
	}

	if err != nil {
		log.Infof("NewVpnClient() failed to create device: %v", err)
		return fmt.Errorf("failed to create %s device: %w", protocol, err)
	}

	client = core.NewClient(device, tunnelFD, mtu)
	log.Infof("NewVpnClient(): core client allocated status=%s", client.Status())

	log.Infof("NewVpnClient() finished")
	return nil
}

func VpnConnect() (err error) {
	defer guardExportErr("VpnConnect", &err)()
	log.Infof("VpnConnect() called")

	if client == nil {
		log.Infof("VpnConnect() failed: client is nil")
		return fmt.Errorf("VpnConnect(): client is nil")
	}

	log.Infof("VpnConnect(): before Connect status=%s", client.Status())
	if err := client.Connect(); err != nil {
		log.Infof("VpnConnect() failed: %v", err)
		return fmt.Errorf("VpnConnect(): %w", err)
	}

	log.Infof("VpnConnect() finished successfully status=%s", client.Status())
	return nil
}

func VpnDisconnect() (err error) {
	defer guardExportErr("VpnDisconnect", &err)()
	log.Infof("VpnDisconnect() called")

	if client == nil {
		log.Infof("VpnDisconnect(): client already nil")
		return nil
	}

	log.Infof("VpnDisconnect(): before Disconnect status=%s", client.Status())
	if err := client.Disconnect(); err != nil {
		log.Infof("VpnDisconnect(): client disconnect failed: %v", err)
		return err
	}
	client = nil

	log.Infof("VpnDisconnect() finished")
	return nil
}

func VpnStatus() (status string, err error) {
	defer guardExportErr("VpnStatus", &err)()

	if client == nil {
		log.Infof("VpnStatus: client=nil")
		return "client=false engineStarted=false fd=-1 deviceNil=true reason=client_nil", nil
	}

	status = client.Status()
	log.Infof("VpnStatus: %s", status)
	return status, nil
}
