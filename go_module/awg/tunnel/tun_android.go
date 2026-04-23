//go:build android

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

func (a *TunnelData) Run() error {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Infof,
	}

	tun, name, err := tun.CreateUnmonitoredTUNFromFD(a.InterfaceFD)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed create unmonitored TUN from FD: %v", err)
	}

	logger.Verbosef("Attaching to interface %v", name)
	a.device = device.NewDevice(tun, conn.NewStdNetBind(), logger)

	uapiConfig, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed get IPC config: %v", err)
	}

	err = a.device.IpcSet(uapiConfig)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed to set IPC config: %v", err)
	}
	a.device.DisableSomeRoamingForBrokenMobileSemantics()

	err = a.device.Up()
	if err != nil {
		a.device.Close()
		return fmt.Errorf("Failed to bring peers up: %v", err)
	}

	logger.Verbosef("Device started")

	return nil
}

func (a *TunnelData) Stop() {
	if a.device != nil {
		a.device.Close()
	}
}

func (a *TunnelData) GetSocketV4() int {
	bind, _ := a.device.Bind().(conn.PeekLookAtSocketFd)
	if bind == nil {
		return -1
	}
	fd, err := bind.PeekLookAtSocketFd4()
	if err != nil {
		return -1
	}
	return fd
}

func (a *TunnelData) GetSocketV6() int {
	bind, _ := a.device.Bind().(conn.PeekLookAtSocketFd)
	if bind == nil {
		return -1
	}
	fd, err := bind.PeekLookAtSocketFd6()
	if err != nil {
		return -1
	}
	return fd
}
