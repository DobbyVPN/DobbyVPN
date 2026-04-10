//go:build linux && !(android || ios)

package internal

import (
	"errors"
	"fmt"
	"os"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/songgao/water"
	"github.com/vishvananda/netlink"

	"go_module/log"
	"go_module/tunnel/protected_dialer"
)

type tunDevice struct {
	*water.Interface
	link netlink.Link
}

var _ network.IPDevice = (*tunDevice)(nil)

func newTunDevice(name, ip string) (d network.IPDevice, err error) {
	log.Infof("[TUN][Init] Creating TUN device name=%s ip=%s", name, ip)

	if name == "" {
		err = errors.New("name is required for TUN/TAP device")
		log.Infof("[TUN][Init][ERROR] %v", err)
		return nil, err
	}
	if ip == "" {
		err = errors.New("ip is required for TUN/TAP device")
		log.Infof("[TUN][Init][ERROR] %v", err)
		return nil, err
	}

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
			_ = tun.Close()
		}
	}()

	tunLink, err := netlink.LinkByName(name)
	if err != nil {
		err = fmt.Errorf("newly created TUN/TAP device '%s' not found: %w", name, err)
		log.Infof("[TUN][Netlink][ERROR] %v", err)
		return nil, err
	}

	tunDev := &tunDevice{tun, tunLink}

	if err = tunDev.configureSubnet(ip); err != nil {
		err = fmt.Errorf("failed to configure TUN/TAP device subnet: %w", err)
		log.Infof("[TUN][Config][ERROR] %v", err)
		return nil, err
	}

	if err = tunDev.bringUp(); err != nil {
		err = fmt.Errorf("failed to bring up TUN/TAP device: %w", err)
		log.Infof("[TUN][Link][ERROR] %v", err)
		return nil, err
	}

	return tunDev, nil
}

func (d *tunDevice) MTU() int { return 1500 }

func (d *tunDevice) configureSubnet(ip string) error {
	subnet := ip + "/32"

	addr, err := netlink.ParseAddr(subnet)
	if err != nil {
		return fmt.Errorf("subnet address '%s' is not valid: %w", subnet, err)
	}

	if err = netlink.AddrAdd(d.link, addr); err != nil {
		return fmt.Errorf("failed to add subnet to TUN/TAP device '%s': %w", d.Name(), err)
	}

	return nil
}

func (d *tunDevice) bringUp() error {
	if err := netlink.LinkSetUp(d.link); err != nil {
		return fmt.Errorf("failed to bring TUN/TAP device '%s' up: %w", d.Name(), err)
	}
	return nil
}

func (d *tunDevice) GetFd() int {
	if d.Interface == nil || d.ReadWriteCloser == nil {
		return -1
	}

	if f, ok := d.ReadWriteCloser.(*os.File); ok {
		fd, err := protected_dialer.UintptrToInt(f.Fd())
		if err != nil {
			return -1
		}
		return fd
	}

	type fder interface {
		Fd() uintptr
	}
	if f, ok := d.ReadWriteCloser.(fder); ok {
		fd, err := protected_dialer.UintptrToInt(f.Fd())
		if err != nil {
			return -1
		}
		return fd
	}

	return -1
}

