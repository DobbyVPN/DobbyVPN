//go:build android

package tunnel

import (
	"fmt"
	"go_module/log"
	"math"
	"net"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig string
	InterfaceFD     int
	Device          *device.Device
	logger          *device.Logger
	uapi            net.Listener
	errs            chan error
}

type TunnelHandle struct {
	device *device.Device
	uapi   net.Listener
}

var tunnelHandles map[int32]TunnelHandle = make(map[int32]TunnelHandle)

func CreateTunnelData(tun string, conf string) *TunnelData {
	return &TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: conf,
	}
}

func (a *TunnelData) Run() error {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Infof,
	}

	tun, name, err := tun.CreateUnmonitoredTUNFromFD(a.InterfaceFD)
	if err != nil {
		unix.Close(a.InterfaceFD)
		logger.Errorf("CreateUnmonitoredTUNFromFD: %v", err)
		return fmt.Errorf("error")
	}

	logger.Verbosef("Attaching to interface %v", name)
	device := device.NewDevice(tun, conn.NewStdNetBind(), logger)

	err = device.IpcSet(a.InterfaceConfig)
	if err != nil {
		unix.Close(a.InterfaceFD)
		logger.Errorf("IpcSet: %v", err)
		return fmt.Errorf("error")
	}
	device.DisableSomeRoamingForBrokenMobileSemantics()

	var uapi net.Listener

	uapiFile, err := ipc.UAPIOpen(name)
	if err != nil {
		logger.Errorf("UAPIOpen: %v", err)
	} else {
		uapi, err = ipc.UAPIListen(name, uapiFile)
		if err != nil {
			uapiFile.Close()
			logger.Errorf("UAPIListen: %v", err)
		} else {
			go func() {
				for {
					conn, err := uapi.Accept()
					if err != nil {
						return
					}
					go device.IpcHandle(conn)
				}
			}()
		}
	}

	err = device.Up()
	if err != nil {
		logger.Errorf("Unable to bring up device: %v", err)
		uapiFile.Close()
		device.Close()
		return fmt.Errorf("error")
	}
	logger.Verbosef("Device started")

	var i int32
	for i = 0; i < math.MaxInt32; i++ {
		if _, exists := tunnelHandles[i]; !exists {
			break
		}
	}
	if i == math.MaxInt32 {
		logger.Errorf("Unable to find empty handle")
		uapiFile.Close()
		device.Close()
		return fmt.Errorf("error")
	}
	tunnelHandles[i] = TunnelHandle{device: device, uapi: uapi}
	return nil
}

func (a *TunnelData) Stop() {
}
