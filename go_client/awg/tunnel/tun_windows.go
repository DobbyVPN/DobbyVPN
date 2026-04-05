//go:build linux

package tunnel

import (
	"fmt"
	"go_client/awg/config"
	"go_client/log"
	"net"
	"os"
	"os/signal"
	"strconv"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	TunnelDevice    tun.Device
	TunnelBind      conn.Bind
	logger          *device.Logger
	nativeTun       *tun.NativeTun
	dev             *device.Device
	uapiListener    net.Listener
	errs            chan error
	term            chan os.Signal
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
	log.Infof("[AWG] Init")
	a.errs = make(chan error, 1)
	a.term = make(chan os.Signal, 1)
	a.logger = device.NewLogger(device.LogLevelVerbose, fmt.Sprintf("(%s) ", a.InterfaceName))

	log.Infof("[AWG] DeduplicateNetworkEntries")
	a.config.DeduplicateNetworkEntries()

	a.TunnelDevice, err = a.openTun()
	if err != nil {
		return fmt.Errorf("Failed to create TUN device: %s", err)
	}

	a.nativeTun = a.TunnelDevice.(*tun.NativeTun)

	wintunVersion, err := a.nativeTun.RunningVersion()
	if err != nil {
		log.Infof("[AWG] [WARNING] unable to determine Wintun version: %v", err)
	} else {
		log.Infof("[AWG] Using Wintun/%d.%d", (wintunVersion>>16)&0xffff, wintunVersion&0xffff)
	}

	fileUAPI, err := a.openUAPI()
	if err != nil {
		return fmt.Errorf("Failed to open UAPI: %s", err)
	}

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("Failed to convert config to UAPI: %s", err)
	}
	log.Infof("[AWG] [UAPI] %s", uapiConf)

	log.Infof("[AWG] Listening UAPI")
	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %v", err)
	}
	a.uapiListener = uapi

	a.TunnelBind = conn.NewDefaultBind()
	a.dev = device.NewDevice(a.TunnelDevice, a.TunnelBind, a.logger)

	log.Infof("[AWG] Seting up UAPI config")
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("IPC set error: %v", err)
	}

	go a.ipcAcceptLoop()
	go a.tunnelLoop()

	log.Infof("[AWG] Bringing peers up")
	return a.dev.Up()
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")
	if a.uapiListener != nil {
		a.uapiListener.Close()
	}
	if a.dev != nil {
		a.dev.Close()
	}
}

func (a *TunnelData) openTun() (tun.Device, error) {
	log.Infof("[AWG] Create awg TUN device")

	if fdStr := os.Getenv(ENV_WG_TUN_FD); fdStr != "" {
		fd, err := strconv.ParseUint(fdStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse fd unit %s: %s", fdStr, err)
		}
		if err := unix.SetNonblock(int(fd), true); err != nil {
			return nil, fmt.Errorf("Failed to SetNonblock: %s", err)
		}
		file := os.NewFile(uintptr(fd), "")
		return tun.CreateTUNFromFile(file, device.DefaultMTU)
	}
	return tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
}

func (a *TunnelData) openUAPI() (*os.File, error) {
	log.Infof("[AWG] Open UAPI")

	if fdStr := os.Getenv(ENV_WG_UAPI_FD); fdStr != "" {
		fd, err := strconv.ParseUint(fdStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse fd unit %s: %s", fdStr, err)
		}
		return os.NewFile(uintptr(fd), ""), nil
	}
	return ipc.UAPIOpen(a.InterfaceName)
}

func (a *TunnelData) ipcAcceptLoop() {
	log.Infof("Running IPC accept loop")

	for {
		c, err := a.uapiListener.Accept()
		if err != nil {
			a.errs <- err
			return
		}
		go a.dev.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Infof("Running tunnel loop")

	signal.Notify(a.term, unix.SIGTERM, os.Interrupt)

	select {
	case <-a.term:
	case err := <-a.errs:
		log.Infof("[ERROR] Got error: %s", err)

		return
	case <-a.dev.Wait():
	}

	a.Stop()
}
