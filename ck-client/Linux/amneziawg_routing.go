//go:build linux
// +build linux

package main

import (
	"net"
	"os"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"

	sysctl "github.com/lorenzosaino/go-sysctl"
)

const tunnelServicePath = "libs/tunnel-service"
const tunnelLogLevel = "debug"

func setUpInterface(configFilePath, interfaceName string) error {
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return err
	}

	return netlink.LinkSetUp(link)
}

func addAddresses(config *Config, interfaceName string) error {
	for _, address := range config.Interface.Addresses {
		if err := addAddress(interfaceName, address.String()); err != nil {
			return err
		}
	}

	return nil
}

func addAddress(interfaceName, address string) error {
	Logging.Info.Printf("Add address %s %s", interfaceName, address)
	// sudo ip -4 address add <address> dev <interfaceName>
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return err
	}

	addr, err := netlink.ParseAddr(address)
	if err != nil {
		return err
	}

	return netlink.AddrAdd(link, addr)
}

func addRoutes(config *Config, interfaceName string) error {
	for _, peer := range config.Peers {
		for _, allowed_ip := range peer.AllowedIPs {
			if err := addRoute(config, interfaceName, allowed_ip.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func addRoute(config *Config, interfaceName, address string) error {
	Logging.Info.Printf("Add route for %s %s", interfaceName, address)
	// sudo ip rule add not fwmark <table> table <table>
	ruleNot := netlink.NewRule()
	ruleNot.Invert = true
	ruleNot.Mark = uint32(config.Interface.FwMark)
	ruleNot.Table = int(config.Interface.FwMark)
	if err := netlink.RuleAdd(ruleNot); err != nil {
		return err
	}

	// sudo ip rule add table main suppress_prefixlength 0
	ruleAdd := netlink.NewRule()
	ruleAdd.Table = unix.RT_TABLE_MAIN
	ruleAdd.SuppressPrefixlen = 0
	if err := netlink.RuleAdd(ruleAdd); err != nil {
		return err
	}

	// sudo ip route add <address> dev <interfaceName> table <table>
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return err
	}

	_, dst, err := net.ParseCIDR(address)
	if err != nil {
		return err
	}

	route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst, Table: int(config.Interface.FwMark)}

	if err := netlink.RouteAdd(&route); err != nil {
		return err
	}

	// sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1
	if err := sysctl.Set("net.ipv4.conf.all.src_valid_mark", "1"); err != nil {
		return err
	}

	return nil
}

func runTunnelService(configFilePath, interfaceName string) error {
	devNullFile, _ := os.Open(os.DevNull)
	attr := &os.ProcAttr{
		Files: []*os.File{
			devNullFile, // stdin
			os.Stdout,   // stdout
			os.Stderr,   // stderr
		},
		Dir: ".",
		Env: os.Environ(),
	}

	process, err := os.StartProcess(
		tunnelServicePath,
		[]string{
			tunnelServicePath,
			interfaceName,
			configFilePath,
			tunnelLogLevel,
		},
		attr,
	)

	if err != nil {
		return err
	} else {
		process.Release()
		return nil
	}
}

func readConfig(configFilePath, interfaceName string) (*Config, error) {
	configData, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}

	configDataString := string(configData)

	Logging.Info.Printf("%s", configDataString)

	return FromWgQuickWithUnknownEncoding(configDataString, interfaceName)
}

// Installs AmneziaWG tunnel with the provided config and tunnel name
func installTunnel(configFilePath, interfaceName string) error {
	err := runTunnelService(configFilePath, interfaceName)
	if err != nil {
		return err
	} else {
		Logging.Info.Printf("Tunnel initialisation success")
	}

	// Wait for service to run
	time.Sleep(100 * time.Millisecond)

	if setUpInterface(configFilePath, interfaceName); err != nil {
		return err
	} else {
		Logging.Info.Printf("Interface set up success")
	}

	config, err := readConfig(configFilePath, interfaceName)

	if addAddresses(config, interfaceName); err != nil {
		return err
	} else {
		Logging.Info.Printf("Interface addresses addition success")
	}

	if addRoutes(config, interfaceName); err != nil {
		return err
	} else {
		Logging.Info.Printf("Interface routing initialisation success")
	}

	return nil
}

func uninstallTunnel(interfaceName string) error {
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return err
	}

	return netlink.LinkDel(link)
}
