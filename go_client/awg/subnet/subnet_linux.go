//go:build linux

package subnet

import (
	"fmt"
	"net"

	"go_client/awg/config"
	"go_client/log"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	sysctl "github.com/lorenzosaino/go-sysctl"
	"github.com/vishvananda/netlink"

	"golang.org/x/sys/unix"
)

type SubnetData struct {
	InterfaceName string
	Config        *config.Config
}

func CreateSubnetData(tun string, conf *config.Config, tdev tun.Device, bind conn.Bind) *SubnetData {
	return &SubnetData{
		InterfaceName: tun,
		Config:        conf,
	}
}

func (subnet *SubnetData) ConfigureSubnet() error {
	var err error

	if err = subnet.setUpInterface(); err != nil {
		return err
	}

	if subnet.addAddresses(); err != nil {
		return err
	}

	if subnet.addRoutes(); err != nil {
		return err
	}

	return nil
}

func (subnet *SubnetData) setUpInterface() error {
	log.Infof("[AWG] Setting up %s interface", subnet.InterfaceName)

	link, err := netlink.LinkByName(subnet.InterfaceName)
	if err != nil {
		return err
	}

	return netlink.LinkSetUp(link)
}

func (subnet *SubnetData) addAddresses() error {
	log.Infof("[AWG] Adding all addresses")

	for _, address := range subnet.Config.Interface.Addresses {
		if err := subnet.addAddress(address.String()); err != nil {
			return err
		}
	}

	return nil
}

func (subnet *SubnetData) addAddress(address string) error {
	log.Infof("[AWG] Adding address %s dev %s", subnet.InterfaceName, address)

	// sudo ip -4 address add <address> dev <interfaceName>
	link, err := netlink.LinkByName(subnet.InterfaceName)
	if err != nil {
		return fmt.Errorf("Error finding ip link %s: %s", subnet.InterfaceName, err)
	}

	addr, err := netlink.ParseAddr(address)
	if err != nil {
		return fmt.Errorf("Error parsing address %s: %s", address, err)
	}

	err = netlink.AddrAdd(link, addr)
	if err != nil {
		return fmt.Errorf("Error adding address %s, %s: %s", link, addr, err)
	}

	return nil
}

func (subnet *SubnetData) addRoutes() error {
	log.Infof("[AWG] Adding all routes")

	for _, peer := range subnet.Config.Peers {
		for _, allowed_ip := range peer.AllowedIPs {
			if err := subnet.addRoute(allowed_ip.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (subnet *SubnetData) addRoute(address string) error {
	log.Infof("[AWG] Add route for %s %s", subnet.InterfaceName, address)

	// sudo ip rule add not fwmark <table> table <table>
	ruleNot := netlink.NewRule()
	ruleNot.Invert = true
	ruleNot.Mark = 51820
	ruleNot.Table = 51820
	if err := netlink.RuleAdd(ruleNot); err != nil {
		return fmt.Errorf("Error adding rule 'sudo ip rule add not fwmark <table> table <table>': %s", err)
	}

	// sudo ip rule add table main suppress_prefixlength 0
	ruleAdd := netlink.NewRule()
	ruleAdd.Table = unix.RT_TABLE_MAIN
	ruleAdd.SuppressPrefixlen = 0
	if err := netlink.RuleAdd(ruleAdd); err != nil {
		return fmt.Errorf("Error adding rule 'sudo ip rule add table main suppress_prefixlength 0': %s", err)
	}

	// sudo ip route add <address> dev <interfaceName> table <table>
	link, err := netlink.LinkByName(subnet.InterfaceName)
	if err != nil {
		return fmt.Errorf("Error finding net link %s: %s", subnet.InterfaceName, err)
	}

	_, dst, err := net.ParseCIDR(address)
	if err != nil {
		return fmt.Errorf("Error parsing CIDR %s: %s", address, err)
	}

	route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst, Table: 51820}

	if err := netlink.RouteAdd(&route); err != nil {
		return fmt.Errorf("Error adding rule 'routeAdd': %s", err)
	}

	// sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1
	if err := sysctl.Set("net.ipv4.conf.all.src_valid_mark", "1"); err != nil {
		return fmt.Errorf("Error setting sysctl value 'sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1': %s", err)
	}

	return nil
}
