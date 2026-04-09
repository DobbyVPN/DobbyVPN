//go:build android

package tunnel

import (
	"fmt"
	"go_module/awg/config"
	"go_module/log"
	"net"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	InterfaceFD     int
	Device          *device.Device
	logger          *device.Logger
	uapi            net.Listener
	errs            chan error
}

func CreateTunnelData(tun string, conf *config.Config) *TunnelData {
	return &TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: conf,
	}
}

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

const (
	ENV_WG_TUN_FD             = "WG_TUN_FD"
	ENV_WG_UAPI_FD            = "WG_UAPI_FD"
	ENV_WG_PROCESS_FOREGROUND = "WG_PROCESS_FOREGROUND"
)

func (a *TunnelData) Run() error {
	var err error

	log.Infof("[AWG] Running awg tunnel (android)")
	a.errs = make(chan error, 1)

	log.Infof("[AWG] DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Infof("[AWG] Create awg TUN device")
	tdev, name, err := tun.CreateUnmonitoredTUNFromFD(a.InterfaceFD)
	if err != nil {
		return fmt.Errorf("Failed to create TUN device: %s", err)
	}

	log.Infof("[AWG] Attaching to the interface %s", name)

	log.Infof("[AWG] Creating interface instance")
	bind := conn.NewDefaultBind()
	a.Device = device.NewDevice(tdev, bind, &device.Logger{log.Infof, log.Infof})

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("Failed to convert config to UAPI: %s", err)
	}
	log.Infof("[AWG] [UAPI] %s", uapiConf)

	log.Infof("[AWG] Seting up UAPI config")
	err = a.Device.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("IPC set error: %v", err)
	}

	log.Infof("[AWG] Disable some roaming for broken mobile semantics")
	a.Device.DisableSomeRoamingForBrokenMobileSemantics()

	log.Infof("[AWG] Setting interface configuration")
	fileUAPI, err := ipc.UAPIOpen(name)
	if err != nil {
		return fmt.Errorf("Failed to open UAPI file: %s", err)
	}
	uapi, err := ipc.UAPIListen(name, fileUAPI)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %v", err)
	}
	a.uapi = uapi

	log.Infof("[AWG] Bringing peers up")
	err = a.Device.Up()
	if err != nil {
		return fmt.Errorf("Bringing peers up error: %v", err)
	}

	log.Infof("[AWG] IPC accept loop")
	go a.ipcAcceptLoop()

	log.Infof("[AWG] Tunnel loop")
	a.tunnelLoop()

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")

	if a.uapi != nil {
		a.uapi.Close()
	}
	if a.Device != nil {
		a.Device.Close()
	}
}

func (a *TunnelData) ipcAcceptLoop() {
	log.Infof("[AWG] Running IPC accept loop")

	for {
		c, err := a.uapi.Accept()
		if err != nil {
			a.errs <- err

			log.Infof("[AWG] [ERROR] Got IPC error, stopping IPC loop")
			return
		}
		go a.Device.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Infof("[AWG] Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Infof("[AWG] [ERROR] Got error, stopping tunnel loop: %s", err)
		return
	case <-a.Device.Wait():
		log.Infof("[AWG] [WARNING] Device wait call, stopping tunnel loop")
		return
	}
}
