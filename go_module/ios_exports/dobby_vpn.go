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

func guardExport(fnName string) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Infof("%s\n%s", msg, string(debug.Stack()))
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
	defer guardExport("NewVpnClient")()
	log.Infof("NewVpnClient() called protocol=%s fd=%d mtu=%d", protocol, tunnelFD, mtu)

	if client != nil {
		if err := VpnDisconnect(); err != nil {
			return fmt.Errorf("NewVpnClient(): disconnect failed: %w", err)
		}
	}

	if tunnelFD < 0 {
		return fmt.Errorf("NewVpnClient(): invalid tunnel fd %d", tunnelFD)
	}

	log.Infof("Using tunnel fd=%d mtu=%d", tunnelFD, mtu)
	log.Infof("Config length=%d", len(transportConfig))

	var device pkg.ProtocolDevice

	// Factory: Create the protocol-specific device
	switch protocol {
	case "xray":
		device, err = xray.NewXrayDevice(transportConfig)
	case "outline":
		device, err = outline.NewOutlineDeviceWithOptions(transportConfig, outline.DeviceOptions{
			PreferTCPDNSForWebSocket: true,
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

	log.Infof("NewVpnClient() finished")
	return nil
}

func VpnConnect() error {
	defer guardExport("VpnConnect")()
	log.Infof("VpnConnect() called")

	if client == nil {
		return fmt.Errorf("VpnConnect(): client is nil")
	}

	if err := client.Connect(); err != nil {
		log.Infof("VpnConnect() failed: %v", err)
		return fmt.Errorf("VpnConnect(): %w", err)
	}

	log.Infof("VpnConnect() finished successfully")
	return nil
}

func VpnDisconnect() error {
	defer guardExport("VpnDisconnect")()
	log.Infof("VpnDisconnect() called")

	if client == nil {
		return nil
	}

	client.Disconnect()
	client = nil

	log.Infof("VpnDisconnect() finished")
	return nil
}

func VpnStatus() (string, error) {
	defer guardExport("VpnStatus")()

	if client == nil {
		return "client=false engineStarted=false fd=-1 deviceNil=true reason=client_nil", nil
	}

	status := client.Status()
	log.Infof("VpnStatus: %s", status)
	return status, nil
}
