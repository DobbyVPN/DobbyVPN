//go:build ios

package cloak_outline

import (
	"fmt"
	"go_module/core"
	"go_module/log"
	"go_module/vpnmanager"
	"os"

	"golang.org/x/sys/unix"
)

const utunControlName = "com.apple.net.utun_control"
const logCategory = "ios_exports"

var vpnHolder = vpnmanager.NewClientHolder(logCategory)

func NewVpnClient(transportConfig string, protocol string) (err error) {
	defer vpnmanager.GuardErr(logCategory, "NewVpnClient", &err)()
	log.Debugf(logCategory, "NewVpnClient() called")

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	device, switched, err := vpnHolder.SwitchOrPrepareDevice(transportConfig, protocol)
	if err != nil {
		return fmt.Errorf("NewVpnClient(): %w", err)
	}
	if switched {
		return nil
	}

	log.Debugf(logCategory, "Start fd search")

	fd := GetTunnelFileDescriptor()
	if fd < 0 {
		return fmt.Errorf("NewVpnClient(): utun fd not found")
	}

	log.Debugf(logCategory, "Fd was found, fd = %d", fd)
	log.Debugf(logCategory, "Config length=%d", len(transportConfig))

	tunFd, err := unix.Dup(fd)
	if err != nil {
		return fmt.Errorf("NewVpnClient(): failed to duplicate utun fd: %w", err)
	}
	log.Debugf(logCategory, "Duplicated utun fd = %d", tunFd)
	tunFile := os.NewFile(uintptr(tunFd), "utun")

	vpnHolder.SetClient(core.NewClient(device, tunFile))

	log.Debugf(logCategory, "NewVpnClient() finished")
	return nil
}

func VpnConnect() (err error) {
	defer vpnmanager.GuardErr(logCategory, "VpnConnect", &err)()
	log.Debugf(logCategory, "VpnConnect() called")

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	if err := vpnHolder.Connect(); err != nil {
		return fmt.Errorf("VpnConnect(): %w", err)
	}

	log.Debugf(logCategory, "VpnConnect() finished successfully")
	return nil
}

func VpnDisconnect() (err error) {
	defer vpnmanager.GuardErr(logCategory, "VpnDisconnect", &err)()
	log.Debugf(logCategory, "VpnDisconnect() called")

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	if err := vpnHolder.DisconnectAndClear(); err != nil {
		return fmt.Errorf("VpnDisconnect(): %w", err)
	}

	log.Debugf(logCategory, "VpnDisconnect() finished")
	return nil
}

// NewOutlineClient creates an Outline VPN client using the given transport config.
// Equivalent to NewVpnClient(config, "outline").
func NewOutlineClient(transportConfig string) (err error) {
	defer vpnmanager.GuardErr(logCategory, "NewOutlineClient", &err)()
	log.Debugf(logCategory, "NewOutlineClient() called config.len=%d", len(transportConfig))
	return NewVpnClient(transportConfig, "outline")
}

// OutlineConnect connects the previously created Outline client.
func OutlineConnect() (err error) {
	defer vpnmanager.GuardErr(logCategory, "OutlineConnect", &err)()
	log.Debugf(logCategory, "OutlineConnect() called")
	return VpnConnect()
}

// OutlineDisconnect disconnects and tears down the Outline client.
func OutlineDisconnect() (err error) {
	defer vpnmanager.GuardErr(logCategory, "OutlineDisconnect", &err)()
	log.Debugf(logCategory, "OutlineDisconnect() called")
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
