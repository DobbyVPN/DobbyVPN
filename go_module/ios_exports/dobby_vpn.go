//go:build ios

package cloak_outline

import (
	"fmt"
	"go_module/core"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/outline"
	"go_module/xray"
	"os"
	"runtime/debug"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"

var client *core.CoreClient

func guardExportErr(fnName string, errp *error) func() {
	return func() {
		if r := recover(); r != nil {
			msg := "panic in " + fnName + ": " + unsafeToString(r)
			log.Debugf("ios_exports", "%s\n%s", msg, string(debug.Stack()))
			if errp != nil {
				*errp = fmt.Errorf("%s", msg)
			}
		}
	}
}

func unsafeToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprintf("%v", v)
	}
}

func NewVpnClient(transportConfig string, protocol string) (err error) {
	defer guardExportErr("NewVpnClient", &err)()
	log.Debugf("ios_exports", "NewVpnClient() called")

	if client != nil {
		if err := VpnDisconnect(); err != nil {
			log.Debugf("ios_exports", "NewVpnClient(): previous client disconnect failed: %v", err)
			return fmt.Errorf("NewVpnClient(): disconnect failed: %w", err)
		}
	}

	log.Debugf("ios_exports", "Start fd search")

	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return fmt.Errorf("NewVpnClient(): utun fd not found")
	}

	log.Debugf("ios_exports", "Fd was found, fd = %d", fd)
	log.Debugf("ios_exports", "Config length=%d", len(transportConfig))

	tunFd, err := unix.Dup(fd)
	if err != nil {
		return fmt.Errorf("NewVpnClient(): failed to duplicate utun fd: %w", err)
	}
	log.Debugf("ios_exports", "Duplicated utun fd = %d", tunFd)
	tunFile := os.NewFile(uintptr(tunFd), "utun")

	var device pkg.ProtocolDevice

	// Factory: Create the protocol-specific device
	switch protocol {
	case "xray":
		device, err = xray.NewXrayDevice(transportConfig)
	case "outline":
		device, err = outline.NewOutlineDevice(transportConfig)
	default:
		log.Debugf("ios_exports", "NewVpnClient() failed: unsupported protocol")
		return fmt.Errorf("unsupported protocol: %s", protocol)
	}
	if err != nil {
		log.Debugf("ios_exports", "NewVpnClient() failed to create device: %v", err)
		return fmt.Errorf("failed to create %s device: %w", protocol, err)
	}

	client = core.NewClient(device, tunFile)

	log.Debugf("ios_exports", "NewVpnClient() finished")
	return nil
}

func VpnConnect() (err error) {
	defer guardExportErr("VpnConnect", &err)()
	log.Debugf("ios_exports", "VpnConnect() called")

	if client == nil {
		log.Debugf("ios_exports", "VpnConnect() failed: client is nil")
		return fmt.Errorf("VpnConnect(): client is nil")
	}

	if err := client.Connect(); err != nil {
		log.Debugf("ios_exports", "VpnConnect() failed: %v", err)
		return fmt.Errorf("VpnConnect(): %w", err)
	}

	log.Debugf("ios_exports", "VpnConnect() finished successfully")
	return nil
}

func VpnDisconnect() (err error) {
	defer guardExportErr("VpnDisconnect", &err)()
	log.Debugf("ios_exports", "VpnDisconnect() called")
	if client == nil {
		log.Debugf("ios_exports", "VpnDisconnect(): client already nil")
		return nil
	}

	if err := client.Disconnect(); err != nil {
		log.Debugf("ios_exports", "VpnDisconnect(): client disconnect failed: %v", err)
		return fmt.Errorf("VpnDisconnect(): %w", err)
	}
	client = nil

	log.Debugf("ios_exports", "VpnDisconnect() finished")
	return nil
}

// NewOutlineClient creates an Outline VPN client using the given transport config.
// Equivalent to NewVpnClient(config, "outline").
func NewOutlineClient(transportConfig string) (err error) {
	defer guardExportErr("NewOutlineClient", &err)()
	log.Debugf("ios_exports", "NewOutlineClient() called config.len=%d", len(transportConfig))
	return NewVpnClient(transportConfig, "outline")
}

// OutlineConnect connects the previously created Outline client.
func OutlineConnect() (err error) {
	defer guardExportErr("OutlineConnect", &err)()
	log.Debugf("ios_exports", "OutlineConnect() called")
	return VpnConnect()
}

// OutlineDisconnect disconnects and tears down the Outline client.
func OutlineDisconnect() (err error) {
	defer guardExportErr("OutlineDisconnect", &err)()
	log.Debugf("ios_exports", "OutlineDisconnect() called")
	return VpnDisconnect()
}

func GetTunnelFileDescriptor() int {
	ctlInfo := &unix.CtlInfo{}
	copy(ctlInfo.Name[:], utunControlName)

	for fd := 0; fd < 1024; fd++ {
		addr, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}

		addrCTL, ok := addr.(*unix.SockaddrCtl)
		if !ok {
			continue
		}

		if ctlInfo.Id == 0 {
			if err := unix.IoctlCtlInfo(fd, ctlInfo); err != nil {
				continue
			}
		}

		if addrCTL.ID == ctlInfo.Id {
			return fd
		}
	}

	return -1
}
