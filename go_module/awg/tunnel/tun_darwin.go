//go:build darwin

package tunnel

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"

	"go_module/awg/config"
	"go_module/log"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	dev             *device.Device
	uapi            net.Listener
	errs            chan error
}

func CreateTunnelData(tunName string, conf *config.Config) *TunnelData {
	return &TunnelData{
		InterfaceName:   tunName,
		InterfaceConfig: conf,
	}
}

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

func (a *TunnelData) Run() error {
	var err error

	log.Infof("[AWG] Running awg tunnel (darwin)")
	a.errs = make(chan error, 1)

	log.Infof("[AWG] DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("failed to convert config to UAPI: %w", err)
	}
	log.Infof("[AWG] [UAPI] %s", uapiConf)

	log.Infof("[AWG] Create awg TUN device")
	tdev, err := tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}

	log.Infof("[AWG] Creating interface instance")
	bind := conn.NewDefaultBind()
	a.dev = device.NewDevice(tdev, bind, &device.Logger{Verbosef: log.Infof, Errorf: log.Infof})

	log.Infof("[AWG] Setting interface configuration")
	fileUAPI, err := ipc.UAPIOpen(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("failed to open UAPI file: %w", err)
	}
	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %w", err)
	}
	a.uapi = uapi

	log.Infof("[AWG] Seting up UAPI config")
	if err := a.dev.IpcSet(uapiConf); err != nil {
		return fmt.Errorf("IPC set error: %w", err)
	}

	log.Infof("[AWG] Bringing peers up")
	if err := a.dev.Up(); err != nil {
		return fmt.Errorf("bringing peers up error: %w", err)
	}

	log.Infof("[AWG] Setting up darwin interface routes")

	log.Infof("[AWG] Setting up %s interface", a.InterfaceName)
	if err := a.setUpInterface(); err != nil {
		return err
	}

	log.Infof("[AWG] Adding all addresses")
	if err := a.addAddresses(); err != nil {
		return err
	}

	log.Infof("[AWG] Adding all routes")
	if err := a.addRoutes(); err != nil {
		return err
	}

	log.Infof("[AWG] IPC accept loop")
	go a.ipcAcceptLoop()

	log.Infof("[AWG] Tunnel loop")
	go a.tunnelLoop()

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")

	if a.uapi != nil {
		_ = a.uapi.Close()
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
	log.Infof("[AWG] Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Infof("[AWG] [ERROR] Got error, stopping tunnel loop: %s", err)
		return
	case <-a.dev.Wait():
		log.Infof("[AWG] [WARNING] Device wait call, stopping tunnel loop")
		return
	}
}

func (a *TunnelData) setUpInterface() error {
	log.Infof("Setting up interface")
	cmd := exec.CommandContext(context.Background(), "ifconfig", a.InterfaceName, "up") // #nosec G204 interface name is created by the app/tun layer.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add interface: %w\nOutput: %s", err, output)
	}
	return nil
}

func (a *TunnelData) addAddresses() error {
	for _, address := range a.InterfaceConfig.Interface.Addresses {
		if err := a.addAddress(address.String()); err != nil {
			return err
		}
	}

	return nil
}

func (a *TunnelData) addAddress(address string) error {
	log.Infof("Adding address %s", address)
	cmd := exec.CommandContext(context.Background(), "ifconfig", a.InterfaceName, "inet", address, "alias") // #nosec G204 address comes from parsed AWG config.
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to add alias: %w\nOutput: %s", err, output)
	}
	return nil
}

func (a *TunnelData) addRoutes() error {
	for _, peer := range a.InterfaceConfig.Peers {
		for _, allowedIP := range peer.AllowedIPs {
			if err := a.addRoute(allowedIP.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *TunnelData) addRoute(address string) error {
	log.Infof("Adding routing to %s", address)

	if strings.HasSuffix(address, "/0") {
		cmd := exec.CommandContext(context.Background(), "route", "-q", "-n", "add", "-inet", "0.0.0.0/1", "-interface", a.InterfaceName) // #nosec G204 interface name is created by the app/tun layer.
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to create route: %w", err)
		}

		cmd = exec.CommandContext(context.Background(), "route", "-q", "-n", "add", "-inet", "128.0.0.0/1", "-interface", a.InterfaceName) // #nosec G204 interface name is created by the app/tun layer.
		err = cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to create route: %w", err)
		}
	} else {
		cmd := exec.CommandContext(context.Background(), "route", "-q", "-n", "add", "-inet", address, "-interface", a.InterfaceName) // #nosec G204 address comes from parsed AWG config.
		err := cmd.Run()
		if err != nil {
			return fmt.Errorf("failed to create route: %w", err)
		}
	}
	return nil
}
