//go:build ios

package awg

import "C"
import (
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	_ "go_client/logger"
	"golang.org/x/sys/unix"
	"math"
	"os"
	"runtime/debug"
	"strings"
)

const Name = "awg"

type AwgClient struct {
	tunFd        int32
	settings     string
	tunnelHandle int32
}

func (c *AwgClient) Connect() error {
	c.tunnelHandle = AwgTurnOn("", c.tunFd, c.settings)
	if c.tunnelHandle == -1 {
		return fmt.Errorf("awgTurnOn failed") // TODO: handle error with more detail
	}
	return nil
}

func (c *AwgClient) Disconnect() error {
	AwgTurnOff(c.tunnelHandle)
	c.tunnelHandle = -1
	return nil
}

func (c *AwgClient) Refresh() error {
	if err := c.Disconnect(); err != nil {
		return err
	} // TODO: handle error with more detail
	return c.Connect()
}

type tunnelHandle struct {
	*device.Device
	*device.Logger
}

var tunnelHandles = make(map[int32]tunnelHandle)

//func init() {
//	signals := make(chan os.Signal)
//	signal.Notify(signals, unix.SIGUSR2)
//	go func() {
//		buf := make([]byte, os.Getpagesize())
//		for {
//			select {
//			case <-signals:
//				n := runtime.Stack(buf, true)
//				buf[n] = 0
//				if uintptr(loggerFunc) != 0 {
//					C.callLogger(loggerFunc, loggerCtx, 0, (*C.char)(unsafe.Pointer(&buf[0])))
//				}
//			}
//		}
//	}()
//}

func AwgTurnOn(_ string, tunFd int32, settings string) int32 {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Errorf,
	}
	dupTunFd, err := unix.Dup(int(tunFd))
	if err != nil {
		logger.Errorf("Unable to dup tun fd: %v", err)
		return -1
	}

	err = unix.SetNonblock(dupTunFd, true)
	if err != nil {
		logger.Errorf("Unable to set tun fd as non blocking: %v", err)
		unix.Close(dupTunFd)
		return -1
	}
	tun, err := tun.CreateTUNFromFile(os.NewFile(uintptr(dupTunFd), "/dev/tun"), 0)
	if err != nil {
		logger.Errorf("Unable to create new tun device from fd: %v", err)
		unix.Close(dupTunFd)
		return -1
	}
	logger.Verbosef("Attaching to interface")
	dev := device.NewDevice(tun, conn.NewStdNetBind(), logger)

	err = dev.IpcSet(C.GoString(settings))
	if err != nil {
		logger.Errorf("Unable to set IPC settings: %v", err)
		unix.Close(dupTunFd)
		return -1
	}

	dev.Up()
	logger.Verbosef("Device started")

	var i int32
	for i = 0; i < math.MaxInt32; i++ {
		if _, exists := tunnelHandles[i]; !exists {
			break
		}
	}
	if i == math.MaxInt32 {
		unix.Close(dupTunFd)
		return -1
	}
	tunnelHandles[i] = tunnelHandle{dev, logger}

	common.Client.SetVpnClient(Name, &AwgClient{tunFd: tunFd, settings: settings, tunnelHandle: i})
	common.Client.MarkActive(Name)

	return i
}

func AwgTurnOff(tunnelHandle int32) {
	defer common.Client.MarkInactive(Name)
	dev, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return
	}
	delete(tunnelHandles, tunnelHandle)
	dev.Close()
}

func AwgGetConfig(tunnelHandle int32) string {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return ""
	}
	settings, err := handle.IpcGet()
	if err != nil {
		return ""
	}
	return settings
}

func AwgGetSocketV4(tunnelHandle int32) int32 {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return -1
	}
	bind, _ := handle.Device.Bind().(conn.PeekLookAtSocketFd)
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
	bind, _ := handle.Device.Bind().(conn.PeekLookAtSocketFd)
	if bind == nil {
		return -1
	}
	fd, err := bind.PeekLookAtSocketFd6()
	if err != nil {
		return -1
	}
	return int32(fd)
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
