//go:build android || ios

package awg

import (
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	_ "go_client/logger"
	"math"
	"net"
	"runtime/debug"
	"strings"
)

const Name = "awg"

type mobileDriver struct {
	interfaceName string
	tunFd         int32
	settings      string
	tunnelHandle  int32
}

func newDriver(config Config) (Driver, error) {
	return &mobileDriver{
		tunFd:    config.TunFd,
		settings: config.Settings,
	}, nil
}

func (d *mobileDriver) Connect() error {
	d.tunnelHandle = AwgTurnOn(d.interfaceName, d.tunFd, d.settings)
	if d.tunnelHandle == -1 {
		return fmt.Errorf("awgTurnOn failed") // TODO: handle error with more detail
	}
	return nil
}

func (d *mobileDriver) Disconnect() error {
	AwgTurnOff(d.tunnelHandle)
	d.tunnelHandle = -1
	return nil
}

func (d *mobileDriver) Refresh() error {
	if err := d.Disconnect(); err != nil {
		return err
	} // TODO: handle error with more detail
	return d.Connect()
}

type TunnelHandle struct {
	device *device.Device
	uapi   net.Listener
}

var tunnelHandles = make(map[int32]TunnelHandle)

func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Errorf,
	}

	tun, name, err := createTUN(int(tunFd))
	if err != nil {
		log.Errorf("createTUN: %v", err)
		return -1
	}

	logger.Verbosef("Attaching to interface %v", name)
	device := device.NewDevice(tun, conn.NewStdNetBind(), logger)

	err = device.IpcSet(settings)
	if err != nil {
		log.Errorf("IpcSet: %v", err)
		return -1
	}
	device.DisableSomeRoamingForBrokenMobileSemantics()

	var uapi net.Listener

	uapiFile, err := ipc.UAPIOpen(name)
	if err != nil {
		logger.Errorf("UAPIOpen: %v", err)
		// Even if UAPI fails, we can continue without it.
	} else {
		uapi, err = ipc.UAPIListen(name, uapiFile)
		if err != nil {
			uapiFile.Close()
			logger.Errorf("UAPIListen: %v", err)
			// Even if UAPI fails, we can continue without it.
		} else {
			go func() {
				for {
					conn, err := uapi.Accept()
					if err != nil {
						logger.Errorf("UAPI Accept: %v", err)
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
		if uapiFile != nil {
			uapiFile.Close()
		}
		device.Close()
		return -1
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
		if uapiFile != nil {
			uapiFile.Close()
		}
		device.Close()
		return -1
	}
	tunnelHandles[i] = TunnelHandle{device: device, uapi: uapi}

	common.Client.SetVpnClient(Name, &mobileDriver{interfaceName: interfaceName, tunFd: tunFd, settings: settings, tunnelHandle: i})
	common.Client.MarkActive(Name)

	return i
}

func AwgTurnOff(tunnelHandle int32) {
	defer common.Client.MarkInactive(Name)
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return
	}
	delete(tunnelHandles, tunnelHandle)
	if handle.uapi != nil {
		handle.uapi.Close()
	}
	handle.device.Close()
}

func AwgGetSocketV4(tunnelHandle int32) int32 {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return -1
	}
	bind, _ := handle.device.Bind().(conn.PeekLookAtSocketFd)
	if bind == nil {
		return -1
	}
	fd, err := bind.PeekLookAtSocketFd4()
	if err != nil {
		return -1
	}
	return int32(fd)
}

func AwgGetSocketV6(tunnelHandle int32) int32 {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return -1
	}
	bind, _ := handle.device.Bind().(conn.PeekLookAtSocketFd)
	if bind == nil {
		return -1
	}
	fd, err := bind.PeekLookAtSocketFd6()
	if err != nil {
		return -1
	}
	return int32(fd)
}

func AwgGetConfig(tunnelHandle int32) (string, error) {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return "", fmt.Errorf("invalid tunnel handle: %d", tunnelHandle)
	}
	settings, err := handle.device.IpcGet()
	if err != nil {
		return "", err
	}
	return settings, nil
}

func AwgVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, dep := range info.Deps {
		if dep.Path == "github.com/amnezia-vpn/amneziawg-go" {
			parts := strings.Split(dep.Version, "-")
			if len(parts) == 3 && len(parts[2]) == 12 {
				return parts[2][:7]
			}
			return dep.Version
		}
	}
	return "unknown"
}
