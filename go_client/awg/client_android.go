//go:build android

package awg

import (
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	_ "go_client/logger"
	"golang.org/x/sys/unix"
	"math"
	"net"
	"runtime/debug"
	"strings"
)

const Name = "awg"

type AwgClient struct {
	interfaceName string
	tunFd         int32
	settings      string
	tunnelHandle  int32
}

func (c *AwgClient) Connect() error {
	c.tunnelHandle = AwgTurnOn(c.interfaceName, c.tunFd, c.settings)
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

type TunnelHandle struct {
	device *device.Device
	uapi   net.Listener
}

var tunnelHandles = make(map[int32]TunnelHandle)

//func init() {
//	tunnelHandles = make(map[int32]TunnelHandle)
//	signals := make(chan os.Signal)
//	signal.Notify(signals, unix.SIGUSR2)
//	go func() {
//		buf := make([]byte, os.Getpagesize())
//		for {
//			select {
//			case <-signals:
//				n := runtime.Stack(buf, true)
//				if n == len(buf) {
//					n--
//				}
//				buf[n] = 0
//				C.__android_log_write(C.ANDROID_LOG_ERROR, cstring("AmneziaWG/Stacktrace"), (*C.char)(unsafe.Pointer(&buf[0])))
//			}
//		}
//	}()
//}

func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Errorf,
	}

	tun, name, err := tun.CreateUnmonitoredTUNFromFD(int(tunFd))
	if err != nil {
		unix.Close(int(tunFd))
		logger.Errorf("CreateUnmonitoredTUNFromFD: %v", err)
		return -1
	}

	logger.Verbosef("Attaching to interface %v", name)
	device := device.NewDevice(tun, conn.NewStdNetBind(), logger)

	err = device.IpcSet(settings)
	if err != nil {
		unix.Close(int(tunFd))
		logger.Errorf("IpcSet: %v", err)
		return -1
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
		uapiFile.Close()
		device.Close()
		return -1
	}
	tunnelHandles[i] = TunnelHandle{device: device, uapi: uapi}

	common.Client.SetVpnClient(Name, &AwgClient{interfaceName: interfaceName, tunFd: tunFd, settings: settings, tunnelHandle: i})
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

func AwgGetConfig(tunnelHandle int32) string {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return ""
	}
	settings, err := handle.device.IpcGet()
	if err != nil {
		return ""
	}
	return settings
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

//func AwgGetConfig(tunnelHandle int32) *C.char {
//	handle, ok := tunnelHandles[tunnelHandle]
//	if !ok {
//		return nil
//	}
//	settings, err := handle.device.IpcGet()
//	if err != nil {
//		return nil
//	}
//	return C.CString(settings)
//}
//
//func AwgVersion() *C.char {
//	info, ok := debug.ReadBuildInfo()
//	if !ok {
//		return C.CString("unknown")
//	}
//	for _, dep := range info.Deps {
//		if dep.Path == "github.com/amnezia-vpn/amneziawg-go" {
//			parts := strings.Split(dep.Version, "-")
//			if len(parts) == 3 && len(parts[2]) == 12 {
//				return C.CString(parts[2][:7])
//			}
//			return C.CString(dep.Version)
//		}
//	}
//	return C.CString("unknown")
//}
