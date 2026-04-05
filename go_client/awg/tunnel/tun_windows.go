//go:build windows

package tunnel

import (
	"fmt"
	"go_client/awg/config"
	"go_client/log"
	"net"
	"os"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/amnezia-vpn/amneziawg-windows/conf"
	"github.com/amnezia-vpn/amneziawg-windows/elevate"
	"github.com/amnezia-vpn/amneziawg-windows/tunnel"
	"github.com/amnezia-vpn/amneziawg-windows/version"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	dev             *device.Device
	uapi            net.Listener
	watcher         *interfaceWatcher
	nativeTun       *tun.NativeTun
	config          *conf.Config
	errs            chan error
}

func CreateTunnelData(tun string, conf *config.Config) *TunnelData {
	return &TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: conf,
	}
}

func (a *TunnelData) Run() error {
	var err error

	log.Infof("[AWG] Running awg tunnel (windows)")
	a.errs = make(chan error, 1)

	log.Infof("[AWG] DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("Failed to convert config to UAPI: %s", err)
	}

	log.Infof("[AWG] Getting current executable")
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Cannot get current executable: %v", err)
	}

	log.Infof("[AWG] CopyConfigOwnerToIPCSecurityDescriptor")
	err = tunnel.CopyConfigOwnerToIPCSecurityDescriptor(path)
	if err != nil {
		return fmt.Errorf("Cannot copy config owner to IPC security descriptor: %v", err)
	}

	log.Infof("[AWG] Starting %v", version.UserAgent())

	log.Infof("[AWG] Watching network interfaces")
	watcher, err := watchInterface()
	if err != nil {
		return fmt.Errorf("Cannot watch interface: %v", err)
	}
	a.watcher = watcher

	log.Infof("[AWG] Create awg TUN device")
	wintun, err := tun.CreateTUNWithRequestedGUID(a.InterfaceName, deterministicGUID(a.InterfaceConfig), 0)
	if err != nil {
		return fmt.Errorf("Failed to create TUN device: %s", err)
	}
	a.nativeTun = wintun.(*tun.NativeTun)

	wintunVersion, err := a.nativeTun.RunningVersion()
	if err != nil {
		log.Infof("[AWG] [WARNING] unable to determine Wintun version: %v", err)
	} else {
		log.Infof("[AWG] Using Wintun/%d.%d", (wintunVersion>>16)&0xffff, wintunVersion&0xffff)
	}

	log.Infof("[AWG] Enable firewall")
	err = enableFirewall(a.InterfaceConfig, a.nativeTun)
	if err != nil {
		return fmt.Errorf("Cannot enable firewall: %v", err)
	}

	log.Infof("[AWG] Dropping privileges")
	err = elevate.DropAllPrivileges(true)
	if err != nil {
		return fmt.Errorf("Cannot drop all privileges: %v", err)
	}

	log.Infof("[AWG] Creating interface instance")
	bind := conn.NewDefaultBind()
	a.dev = device.NewDevice(wintun, bind, &device.Logger{log.Infof, log.Infof})

	log.Infof("[AWG] Setting interface configuration")
	uapi, err := ipc.UAPIListen(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %v", err)
	}
	a.uapi = uapi

	log.Infof("[AWG] Seting up UAPI config")
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("IPC set error: %v", err)
	}

	log.Infof("[AWG] Bringing peers up")
	err = a.dev.Up()
	if err != nil {
		return fmt.Errorf("Bringing peers up error: %v", err)
	}

	log.Infof("[AWG] Watcher config")
	watcher.Configure(bind.(conn.BindSocketToInterface), a.InterfaceConfig, a.nativeTun)

	log.Infof("[AWG] IPC accept loop")
	go a.ipcAcceptLoop()

	log.Infof("[AWG] Tunnel loop")
	go a.tunnelLoop()

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")

	if a.watcher != nil {
		a.watcher.Destroy()
	}
	if a.uapi != nil {
		a.uapi.Close()
	}
	if a.dev != nil {
		a.dev.Close()
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
		go a.dev.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Infof("Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Infof("[ERROR] Got error, stopping tunnel loop: %s", err)
		return
	case <-a.dev.Wait():
		log.Infof("[WARNING] Device wait call, stopping tunnel loop")
		return
	case err := <-a.watcher.errors:
		log.Infof("[ERROR] Got watcher error, stopping tunnel loop: %s", err)
		return
	}
}
