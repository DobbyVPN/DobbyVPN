/* SPDX-License-Identifier: Apache-2.0
 *
 * Copyright © 2017-2022 Jason A. Donenfeld <Jason@zx2c4.com>. All Rights Reserved.
 */

package main

// extern int go_protect_socket(int fd);
import "C"

import (
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	"math"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type TunnelHandle struct {
	device *device.Device
}

var tunnelHandles map[int32]TunnelHandle

func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}

	protected_dialer.MakeSocketProtected = func(fd uintptr) {
		C.go_protect_socket(C.int(fd))
	}

	tunnelHandles = make(map[int32]TunnelHandle)
}

//export AwgTurnOn
func AwgTurnOn(interfaceName string, tunFd int32, settings string) int32 {
	logger := &device.Logger{
		Verbosef: log.Infof,
		Errorf:   log.Infof,
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

	err = device.Up()
	if err != nil {
		logger.Errorf("Unable to bring up device: %v", err)
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
		device.Close()
		return -1
	}
	tunnelHandles[i] = TunnelHandle{device: device}
	return i
}

//export AwgTurnOff
func AwgTurnOff(tunnelHandle int32) {
	handle, ok := tunnelHandles[tunnelHandle]
	if !ok {
		return
	}
	delete(tunnelHandles, tunnelHandle)
	handle.device.Close()
}

//export AwgGetSocketV4
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

//export AwgGetSocketV6
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
