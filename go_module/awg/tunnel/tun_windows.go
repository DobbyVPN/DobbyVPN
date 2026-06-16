//go:build windows

package tunnel

import (
	"fmt"
	"go_module/awg/config"
	"go_module/log"
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

	log.Infof(Category, "Running awg tunnel (windows)")
	a.errs = make(chan error, 1)

	log.Debugf(Category, "DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Debugf(Category, "Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("Failed to convert config to UAPI: %s", err)
	}

	log.Debugf(Category, "Getting current executable")
	path, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Cannot get current executable: %v", err)
	}

	log.Debugf(Category, "CopyConfigOwnerToIPCSecurityDescriptor")
	err = tunnel.CopyConfigOwnerToIPCSecurityDescriptor(path)
	if err != nil {
		return fmt.Errorf("Cannot copy config owner to IPC security descriptor: %v", err)
	}

	log.Debugf(Category, "Starting %v", version.UserAgent())

	log.Debugf(Category, "Watching network interfaces")
	watcher, err := watchInterface()
	if err != nil {
		return fmt.Errorf("Cannot watch interface: %v", err)
	}
	a.watcher = watcher

	log.Debugf(Category, "Create awg TUN device")
	wintun, err := tun.CreateTUNWithRequestedGUID(a.InterfaceName, deterministicGUID(a.InterfaceConfig), 0)
	if err != nil {
		return fmt.Errorf("Failed to create TUN device: %s", err)
	}
	a.nativeTun = wintun.(*tun.NativeTun)

	wintunVersion, err := a.nativeTun.RunningVersion()
	if err != nil {
		log.Warnf(Category, "Unable to determine Wintun version: %v", err)
	} else {
		log.Debugf(Category, "Using Wintun/%d.%d", (wintunVersion>>16)&0xffff, wintunVersion&0xffff)
	}

	log.Debugf(Category, "Enable firewall")
	err = enableFirewall(a.InterfaceConfig, a.nativeTun)
	if err != nil {
		return fmt.Errorf("Cannot enable firewall: %v", err)
	}

	log.Debugf(Category, "Dropping privileges")
	err = elevate.DropAllPrivileges(true)
	if err != nil {
		return fmt.Errorf("Cannot drop all privileges: %v", err)
	}

	log.Debugf(Category, "Creating interface instance")
	bind := conn.NewDefaultBind()
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			log.Debugf("TUN", format, args...)
		},
		Errorf: func(format string, args ...any) {
			log.Errorf("TUN", format, args...)
		},
	}
	a.dev = device.NewDevice(wintun, bind, logger)

	log.Debugf(Category, "Setting interface configuration")
	uapi, err := ipc.UAPIListen(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %v", err)
	}
	a.uapi = uapi

	log.Debugf(Category, "Seting up UAPI config")
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("IPC set error: %v", err)
	}

	log.Debugf(Category, "Bringing peers up")
	err = a.dev.Up()
	if err != nil {
		return fmt.Errorf("Bringing peers up error: %v", err)
	}

	log.Debugf(Category, "Watcher config")
	watcher.Configure(bind.(conn.BindSocketToInterface), a.InterfaceConfig, a.nativeTun)

	log.Debugf(Category, "IPC accept loop")
	go a.ipcAcceptLoop()

	log.Debugf(Category, "Tunnel loop")
	go a.tunnelLoop()

	log.Infof(Category, "Device started")

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof(Category, "Shutting down")

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
	log.Debugf(Category, "Running IPC accept loop")

	for {
		c, err := a.uapi.Accept()
		if err != nil {
			a.errs <- err

			log.Errorf(Category, "Got IPC error, stopping IPC loop")
			return
		}
		go a.dev.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Debugf(Category, "Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Errorf(Category, "Got error, stopping tunnel loop: %v", err)
		return
	case <-a.dev.Wait():
		log.Warnf(Category, "Device wait call, stopping tunnel loop")
		return
	case err := <-a.watcher.errors:
		log.Errorf(Category, "Got watcher error, stopping tunnel loop: %v", err)
		return
	}
}
