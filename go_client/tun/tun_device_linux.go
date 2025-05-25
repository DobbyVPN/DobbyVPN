//go:build linux
// +build linux

package tun

import (
	"errors"
	"fmt"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"
)

type tunDevice struct {
	*water.Interface
	link netlink.Link
}

var _ network.IPDevice = (*tunDevice)(nil)

func NewTunDevice(name, ip string) (d network.IPDevice, err error) {
	if len(name) == 0 {
		return nil, errors.New("name is required for TUN/TAP device")
	}
	if len(ip) == 0 {
		return nil, errors.New("ip is required for TUN/TAP device")
	}

	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name:    name,
			Persist: false,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create TUN/TAP device: %w", err)
	}

	defer func() {
		if err != nil {
			tun.Close()
		}
	}()

	tunLink, err := netlink.LinkByName(name)
	if err != nil {
		return nil, fmt.Errorf("newly created TUN/TAP device '%s' not found: %w", name, err)
	}

	tunDev := &tunDevice{tun, tunLink}
	if err := tunDev.configureSubnet(ip); err != nil {
		return nil, fmt.Errorf("failed to configure TUN/TAP device subnet: %w", err)
	}
	if err := tunDev.bringUp(); err != nil {
		return nil, fmt.Errorf("failed to bring up TUN/TAP device: %w", err)
	}
	return tunDev, nil
}

func (d *tunDevice) MTU() int {
	return 1500
}

func (d *tunDevice) configureSubnet(ip string) error {
	subnet := ip + "/32"
	addr, err := netlink.ParseAddr(subnet)
	if err != nil {
		return fmt.Errorf("subnet address '%s' is not valid: %w", subnet, err)
	}
	if err := netlink.AddrAdd(d.link, addr); err != nil {
		return fmt.Errorf("failed to add subnet to TUN/TAP device '%s': %w", d.Interface.Name(), err)
	}
	return nil
}

func (d *tunDevice) bringUp() error {
	if err := netlink.LinkSetUp(d.link); err != nil {
		return fmt.Errorf("failed to bring TUN/TAP device '%s' up: %w", d.Interface.Name(), err)
	}
	return nil
}
