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
	log.Infof(Category, "Running awg tunnel (android)")

	log.Infof(Category, "Converting interface config to the UAPI config")
	uapiConfig, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed get IPC config: %v", err)
	}

	log.Infof(Category, "Create awg TUN device")
	tun, name, err := tun.CreateUnmonitoredTUNFromFD(a.InterfaceFD)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed create unmonitored TUN from FD: %v", err)
	}

	log.Infof(Category, "Creating interface instance %s", name)
	bind := conn.NewStdNetBind()
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			log.Debugf("TUN", format, args...)
		},
		Errorf: func(format string, args ...any) {
			log.Errorf("TUN", format, args...)
		},
	}
	a.device = device.NewDevice(tun, bind, logger)

	log.Infof(Category, "Seting up UAPI config")
	err = a.device.IpcSet(uapiConfig)
	if err != nil {
		unix.Close(a.InterfaceFD)
		return fmt.Errorf("Failed to set IPC config: %v", err)
	}

	log.Infof(Category, "Disable some roaming for broken mobile semantics")
	a.device.DisableSomeRoamingForBrokenMobileSemantics()

	log.Infof(Category, "Bringing peers up")
	err = a.device.Up()
	if err != nil {
		a.device.Close()
		return fmt.Errorf("Failed to bring peers up: %v", err)
	}

	log.Infof(Category, "Device started")

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof(Category, "Shutting down")

	if a.device != nil {
		a.device.Close()
	}
}

func (a *TunnelData) GetSocketV4() int {
	log.Infof(Category, "GetSocketV4")

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
	log.Infof(Category, "GetSocketV6")

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
