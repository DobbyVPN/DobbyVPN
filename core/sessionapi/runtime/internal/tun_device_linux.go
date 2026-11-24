//go:build linux && !(android || ios)

package internal

import (
	"errors"
	"fmt"
	"net/netip"

	"core/log"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip/link/tun"
)

// A nonnegative descriptor belongs to the caller, including on partial setup.
func (app *App) openLinuxTUN() (int, error) {
	config := app.RoutingConfig
	if config.TunDeviceName == "" {
		return -1, errors.New("name is required for TUN/TAP device")
	}
	ifr, err := unix.NewIfreq(config.TunDeviceName)
	if err != nil {
		return -1, fmt.Errorf("invalid TUN name %q: %w", config.TunDeviceName, err)
	}
	ip, err := netip.ParseAddr(config.TunDeviceIP)
	if err != nil {
		return -1, fmt.Errorf("parse TUN %s IPv4 address %q: %w", config.TunDeviceName, config.TunDeviceIP, err)
	}
	if !ip.Is4() {
		return -1, fmt.Errorf("TUN %s requires an IPv4 address, got %s", config.TunDeviceName, ip)
	}

	log.Debugf(Category, "[TUN][Create] name=%s ip=%s mtu=%d", config.TunDeviceName, config.TunDeviceIP, config.TunDeviceMTU)
	fd, err := tun.Open(config.TunDeviceName)
	if err != nil {
		return -1, fmt.Errorf("create TUN %s: %w", config.TunDeviceName, err)
	}
	// An existing persistent TUN can be reopened under the configured name.
	if err := unix.IoctlSetInt(fd, unix.TUNSETPERSIST, 0); err != nil {
		return fd, fmt.Errorf("clear persistence on TUN %s: %w", config.TunDeviceName, err)
	}
	if err := unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr); err != nil {
		return fd, fmt.Errorf("read assigned name for TUN %s: %w", config.TunDeviceName, err)
	}
	name := ifr.Name()
	log.Debugf(Category, "[TUN][Netlink] resolving interface=%s fd=%d", name, fd)
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fd, fmt.Errorf("resolve new TUN %s: %w", name, err)
	}
	log.Debugf(Category, "[TUN][Config] interface=%s index=%d mtu=%d requested_mtu=%d", name, link.Attrs().Index, link.Attrs().MTU, config.TunDeviceMTU)
	if err := netlink.LinkSetMTU(link, config.TunDeviceMTU); err != nil {
		return fd, fmt.Errorf("set MTU %d on TUN %s: %w", config.TunDeviceMTU, name, err)
	}

	ipv4 := &netlink.Addr{IPNet: netlink.NewIPNet(ip.AsSlice())}
	log.Debugf(Category, "[TUN][IP] adding %s to %s", ipv4, name)
	if err := netlink.AddrAdd(link, ipv4); err != nil {
		return fd, fmt.Errorf("add IPv4 %s to TUN %s: %w", ipv4, name, err)
	}
	ipv6, err := netlink.ParseAddr("fd00:dbb::1/128")
	if err != nil {
		return fd, fmt.Errorf("parse TUN %s IPv6 address: %w", name, err)
	}
	ipv6.Flags = unix.IFA_F_NODAD
	log.Debugf(Category, "[TUN][IP] adding %s to %s", ipv6, name)
	if err := netlink.AddrAdd(link, ipv6); err != nil {
		return fd, fmt.Errorf("add IPv6 %s to TUN %s: %w", ipv6, name, err)
	}
	log.Debugf(Category, "[TUN][Link] bringing up %s", name)
	if err := netlink.LinkSetUp(link); err != nil {
		return fd, fmt.Errorf("bring up TUN %s: %w", name, err)
	}
	app.retainDesktopTunnelPolicy(map[string]any{
		"interface":                        name,
		"interface_index":                  link.Attrs().Index,
		"ipv4_prefix":                      ipv4.IPNet.String(),
		"ipv6_prefix":                      ipv6.IPNet.String(),
		"mtu":                              config.TunDeviceMTU,
		"requested_routing_table_id":       config.RoutingTableID,
		"requested_routing_table_priority": config.RoutingTablePriority,
		"requested_dns_servers":            config.DNSServers,
	})
	return fd, nil
}
