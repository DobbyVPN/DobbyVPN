//go:build windows

package internal

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/amnezia-vpn/amneziawg-windows/conf"
	"github.com/amnezia-vpn/amneziawg-windows/elevate"
	"github.com/amnezia-vpn/amneziawg-windows/tunnel"
	"github.com/amnezia-vpn/amneziawg-windows/version"
)

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

type App struct {
	InterfaceName   string
	InterfaceConfig string
	config          *conf.Config
	logger          *device.Logger
	nativeTun       *tun.NativeTun
	dev             *device.Device
	uapiListener    net.Listener
	watcher         *interfaceWatcher
	errs            chan error
	term            chan os.Signal
}

// NewApp creates a new App that will run on Windows.
func NewApp(interfaceName, awgq_config string) (*App, error) {
	iface := strings.TrimSpace(interfaceName)
	if len(iface) == 0 {
		return nil, fmt.Errorf("interface name is required")
	}

	app := &App{
		InterfaceName:   iface,
		InterfaceConfig: awgq_config,
		errs:            make(chan error, 1),
		term:            make(chan os.Signal, 1),
	}

	app.logger = device.NewLogger(device.LogLevelVerbose, fmt.Sprintf("(%s) ", app.InterfaceName))

	return app, nil
}

func (a *App) Run() error {
	var err error

	defer a.Stop()

	a.config, err = conf.FromWgQuickWithUnknownEncoding(a.InterfaceConfig, a.InterfaceName)
	if err != nil {
		return err
	}

	a.config.DeduplicateNetworkEntries()
	path, err := os.Executable()
	if err != nil {
		return err
	}
	err = tunnel.CopyConfigOwnerToIPCSecurityDescriptor(path)
	if err != nil {
		return err
	}
	a.logger.Verbosef("Starting %v", version.UserAgent())

	a.logger.Verbosef("Watching network interfaces")
	a.watcher, err = watchInterface()
	if err != nil {
		return err
	}

	a.logger.Verbosef("Resolving DNS names")
	uapiConf, err := a.config.ToUAPI()
	if err != nil {
		return err
	}

	wintun, err := tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
	if err != nil {
		return err
	}

	a.nativeTun = wintun.(*tun.NativeTun)
	wintunVersion, err := a.nativeTun.RunningVersion()
	if err != nil {
		log.Printf("Warning: unable to determine Wintun version: %v", err)
	} else {
		log.Printf("Using Wintun/%d.%d", (wintunVersion>>16)&0xffff, wintunVersion&0xffff)
	}

	err = enableFirewall(a.config, a.nativeTun)
	if err != nil {
		return err
	}

	a.logger.Verbosef("Dropping privileges")
	err = elevate.DropAllPrivileges(true)
	if err != nil {
		return err
	}

	a.logger.Verbosef("Creating interface instance")
	bind := conn.NewDefaultBind()
	a.dev = device.NewDevice(wintun, bind, &device.Logger{log.Printf, log.Printf})

	a.logger.Verbosef("Setting interface configuration")
	a.uapiListener, err = ipc.UAPIListen(a.config.Name)
	if err != nil {
		return err
	}

	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return err
	}

	a.logger.Verbosef("Bringing peers up")
	a.dev.Up()

	a.watcher.Configure(bind.(conn.BindSocketToInterface), a.config, a.nativeTun)

	a.logger.Verbosef("Listening for UAPI requests")
	go a.acceptLoop()

	select {
	case <-a.term:
	case err := <-a.errs:
		return err
	case <-a.dev.Wait():
	}

	return nil
}

func (a *App) acceptLoop() {
	for {
		c, err := a.uapiListener.Accept()
		if err != nil {
			a.errs <- err
			return
		}
		go a.dev.IpcHandle(c)
	}
}

func (a *App) Stop() {
	if a.watcher != nil {
		a.watcher.Destroy()
	}
	if a.uapiListener != nil {
		a.uapiListener.Close()
	}
	if a.dev != nil {
		a.dev.Close()
	}
	a.logger.Verbosef("Shutting down")
}
