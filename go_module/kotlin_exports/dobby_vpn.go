//go:build android

package dobbyvpn

import (
	"os"
	"strings"

	"go_module/core"
	"go_module/log"
	"go_module/vpnmanager"

	"golang.org/x/sys/unix"
)

const logCategory = "kotlin_exports"

var (
	vpnHolder = vpnmanager.NewClientHolder(logCategory)
	lastError = vpnmanager.NewLastError(logCategory)
)

func GetVpnLastError() string {
	return lastError.Get()
}

func NewVpnClient(config string, protocol string, fd int32) {
	defer vpnmanager.GuardExport(logCategory, "NewVpnClient", lastError)()

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	log.Debugf(logCategory, "NewVpnClient() called")
	lastError.Clear()

	config = strings.Clone(config)
	protocol = strings.Clone(protocol)

	device, switched, err := vpnHolder.SwitchOrPrepareDevice(config, protocol)
	if err != nil {
		lastError.Set(err.Error())
		log.Debugf(logCategory, "NewVpnClient() failed: %v", err)
		closeUnusedTunFD(fd)
		return
	}
	if switched {
		closeUnusedTunFD(fd)
		return
	}

	log.Debugf(logCategory, "NewVpnClient(): config.len=%d protocol=%s fd=%d", len(config), protocol, fd)

	tunFile := os.NewFile(uintptr(fd), "tun")
	if tunFile == nil {
		lastError.Set("failed to create tun file from fd")
		return
	}

	log.Debugf(logCategory, "NewVpnClient(): created device type=%T", device)

	vpnHolder.SetClient(core.NewClient(device, tunFile))

	log.Debugf(logCategory, "NewVpnClient() finished successfully")
}

func VpnConnect() int32 {
	defer vpnmanager.GuardExport(logCategory, "VpnConnect", lastError)()

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	log.Debugf(logCategory, "VpnConnect() called")
	if pendingErr := lastError.Get(); pendingErr != "" {
		log.Debugf(logCategory, "VpnConnect() aborted because NewVpnClient failed: %s", pendingErr)
		return -1
	}
	lastError.Clear()

	if err := vpnHolder.Connect(); err != nil {
		lastError.Set(err.Error())
		return -1
	}

	log.Debugf(logCategory, "VpnConnect() finished successfully")
	return 0
}

func VpnDisconnect() {
	defer vpnmanager.GuardExport(logCategory, "VpnDisconnect", lastError)()

	vpnHolder.Lock()
	defer vpnHolder.Unlock()

	log.Debugf(logCategory, "VpnDisconnect() called")
	_ = vpnHolder.DisconnectAndClear()
	log.Debugf(logCategory, "VpnDisconnect() finished")
}

func closeUnusedTunFD(fd int32) {
	if fd < 0 {
		return
	}
	if err := unix.Close(int(fd)); err != nil {
		log.Debugf(logCategory, "NewVpnClient(): failed to close unused duplicated tun fd=%d: %v", fd, err)
		return
	}
	log.Debugf(logCategory, "NewVpnClient(): closed unused duplicated tun fd=%d", fd)
}
