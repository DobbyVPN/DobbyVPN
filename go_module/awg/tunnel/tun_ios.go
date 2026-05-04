//go:build ios

package tunnel

import (
	"fmt"
	"go_module/awg/config"
	"go_module/log"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	InterfaceFD     int
	device          *device.Device
}

func CreateTunnelData(tun string, conf *config.Config, fd int) *TunnelData {
	return &TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: conf,
		InterfaceFD:     fd,
	}
}

func (a *TunnelData) Run() error {
	log.Infof("[AWG] Running awg tunnel (ios)")

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConfig, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed get IPC config: %v", err)
	}

	log.Infof("[AWG] Create awg TUN device")
	tun, err := tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed create unmonitored TUN from FD: %v", err)
	}

	log.Infof("[AWG] Creating interface instance")
	bind := conn.NewStdNetBind()
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			log.Infof(fmt.Sprintf("[TUN] %s", format), args...)
		},
		Errorf: func(format string, args ...any) {
			log.Infof(fmt.Sprintf("[TUN] [ERROR] %s", format), args...)
		},
	}
	a.device = device.NewDevice(tun, bind, logger)

	log.Infof("[AWG] Seting up UAPI config")
	err = a.device.IpcSet(uapiConfig)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed to set IPC config: %v", err)
	}

	log.Infof("[AWG] Bringing peers up")
	err = a.device.Up()
	if err != nil {
		a.device.Close()
		return fmt.Errorf("Failed to bring peers up: %v", err)
	}

	log.Infof("[AWG] Device started")

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")

	if a.device != nil {
		a.device.Close()
	}
}
