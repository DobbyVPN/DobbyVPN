//go:build linux
// +build linux

package internal

import (
	"errors"
	"fmt"
	"os"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"

	"go_client/log"
)

type tunDevice struct {
	*water.Interface
	link netlink.Link
}

var _ network.IPDevice = (*tunDevice)(nil)

func newTunDevice(name, ip string) (d network.IPDevice, err error) {
	log.Infof("[TUN][Init] Creating TUN device name=%s ip=%s", name, ip)

	if name == "" {
		err := errors.New("name is required for TUN/TAP device")
		log.Infof("[TUN][Init][ERROR] %v", err)
		return nil, err
	}
	if ip == "" {
		err := errors.New("ip is required for TUN/TAP device")
		log.Infof("[TUN][Init][ERROR] %v", err)
		return nil, err
	}

	log.Infof("[TUN][Create] Calling water.New()...")
	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:    name,
			Persist: false,
		},
	})
	if err != nil {
		err = fmt.Errorf("failed to create TUN/TAP device: %w", err)
		log.Infof("[TUN][Create][ERROR] %v", err)
		return nil, err
	}
	log.Infof("[TUN][Create][OK] Interface created: %s", tun.Name())

	defer func() {
		if err != nil {
			log.Infof("[TUN][Cleanup] Closing TUN due to error")
			_ = tun.Close()
		}
	}()

	log.Infof("[TUN][Netlink] Resolving link by name: %s", name)
	tunLink, err := netlink.LinkByName(name)
	if err != nil {
		err = fmt.Errorf("newly created TUN/TAP device '%s' not found: %w", name, err)
		log.Infof("[TUN][Netlink][ERROR] %v", err)
		return nil, err
	}
	log.Infof("[TUN][Netlink][OK] Link found: index=%d mtu=%d",
		tunLink.Attrs().Index,
		tunLink.Attrs().MTU,
	)

	tunDev := &tunDevice{tun, tunLink}

	log.Infof("[TUN][Config] Configuring IP/subnet...")
	if err = tunDev.configureSubnet(ip); err != nil {
		err = fmt.Errorf("failed to configure TUN/TAP device subnet: %w", err)
		log.Infof("[TUN][Config][ERROR] %v", err)
		return nil, err
	}
	log.Infof("[TUN][Config][OK] IP configured")

	log.Infof("[TUN][Link] Bringing interface UP...")
	if err = tunDev.bringUp(); err != nil {
		err = fmt.Errorf("failed to bring up TUN/TAP device: %w", err)
		log.Infof("[TUN][Link][ERROR] %v", err)
		return nil, err
	}
	log.Infof("[TUN][Link][OK] Interface is UP")

	log.Infof("[TUN][Init][SUCCESS] TUN ready: name=%s", name)

	return tunDev, nil
}

func (d *tunDevice) MTU() int {
	return 1500
}

func (d *tunDevice) configureSubnet(ip string) error {
	subnet := ip + "/32"
	log.Infof("[TUN][IP] Adding subnet %s to %s", subnet, d.Name())

	addr, err := netlink.ParseAddr(subnet)
	if err != nil {
		return fmt.Errorf("subnet address '%s' is not valid: %w", subnet, err)
	}

	if err = netlink.AddrAdd(d.link, addr); err != nil {
		return fmt.Errorf("failed to add subnet to TUN/TAP device '%s': %w", d.Name(), err)
	}

	log.Infof("[TUN][IP][OK] Subnet added: %s", subnet)
	return nil
}

func (d *tunDevice) bringUp() error {
	log.Infof("[TUN][Link] Setting interface UP: %s", d.Name())

	if err := netlink.LinkSetUp(d.link); err != nil {
		return fmt.Errorf("failed to bring TUN/TAP device '%s' up: %w", d.Name(), err)
	}

	log.Infof("[TUN][Link][OK] Interface %s is UP", d.Name())
	return nil
}

func (d *tunDevice) GetFd() int {
	log.Infof("[TUN][FD] Extracting file descriptor...")

	if d.Interface == nil {
		log.Infof("[TUN][FD][ERROR] Interface is nil")
		return -1
	}
	if d.ReadWriteCloser == nil {
		log.Infof("[TUN][FD][ERROR] ReadWriteCloser is nil")
		return -1
	}

	// путь 1: *os.File
	if f, ok := d.ReadWriteCloser.(*os.File); ok {
		fd := int(f.Fd())
		log.Infof("[TUN][FD][OK] Got fd via *os.File: %d", fd)
		return fd
	}

	// путь 2: Fd() interface
	type fder interface {
		Fd() uintptr
	}

	if f, ok := d.ReadWriteCloser.(fder); ok {
		fd := int(f.Fd())
		log.Infof("[TUN][FD][OK] Got fd via Fd(): %d", fd)
		return fd
	}

	log.Infof("[TUN][FD][ERROR] Unable to extract fd (unknown type: %T)", d.Interface.ReadWriteCloser)
	return -1
}
