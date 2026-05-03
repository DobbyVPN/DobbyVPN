//go:build linux && !android

package tunnel

import (
	"fmt"
	"go_module/awg/config"
	"go_module/log"
	"net"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"github.com/lorenzosaino/go-sysctl"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

type TunnelData struct {
	InterfaceName   string
	InterfaceConfig *config.Config
	dev             *device.Device
	uapi            net.Listener
	errs            chan error
}

func CreateTunnelData(tunnelName string, tunnelConfig *config.Config) *TunnelData {
	return &TunnelData{
		InterfaceName:   tunnelName,
		InterfaceConfig: tunnelConfig,
	}
}

func (a *TunnelData) Run() error {
	var err error

	log.Debugf(Category, "Running awg tunnel (linux)")
	a.errs = make(chan error, 1)

	log.Debugf(Category, "DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Debugf(Category, "Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("failed to convert config to UAPI: %w", err)
	}

	log.Debugf(Category, "Create awg TUN device")
	tdev, err := tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}

	log.Debugf(Category, "Creating interface instance")
	bind := conn.NewDefaultBind()
	logger := &device.Logger{
		Verbosef: func(format string, args ...any) {
			log.Debugf("TUN", format, args...)
		},
		Errorf: func(format string, args ...any) {
			log.Debugf("TUN", format, args...)
		},
	}
	a.dev = device.NewDevice(tdev, bind, logger)

	log.Debugf(Category, "Setting interface configuration")
	fileUAPI, err := ipc.UAPIOpen(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("failed to open UAPI file: %w", err)
	}
	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		return fmt.Errorf("failed open UAPI listener: %w", err)
	}
	a.uapi = uapi

	log.Debugf(Category, "Seting up UAPI config")
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("failed set IPC: %w", err)
	}

	log.Debugf(Category, "Bringing peers up")
	err = a.dev.Up()
	if err != nil {
		return fmt.Errorf("failed bringing peers up: %w", err)
	}

	log.Debugf(Category, "Setting up linux subnet")

	log.Debugf(Category, "Setting up %s interface", a.InterfaceName)
	if err := a.setUpInterface(); err != nil {
		return fmt.Errorf("failed set up interface: %w", err)
	}

	log.Debugf(Category, "Adding all addresses")
	if err := a.addAddresses(); err != nil {
		return fmt.Errorf("failed add addresses: %w", err)
	}

	log.Debugf(Category, "Adding all routes")
	if err := a.addRoutes(); err != nil {
		return fmt.Errorf("failed add routes: %w", err)
	}

	log.Debugf(Category, "IPC accept loop")
	go a.ipcAcceptLoop()

	log.Debugf(Category, "Tunnel loop")
	go a.tunnelLoop()

	log.Infof(Category, "Device started")

	return nil
}

func (a *TunnelData) Stop() {
	log.Debugf(Category, "Shutting down")

	if a.uapi != nil {
		err := a.uapi.Close()
		if err != nil {
			log.Warnf(Category, "Failed closing UAPI: %v", err)
		}
	}
	if a.dev != nil {
		a.dev.Close()
	}
}

func (a *TunnelData) ipcAcceptLoop() {
	log.Debugf(Category, "Running IPC accept loop")

	for {
		c, err := a.uapi.Accept()
		if err != nil {
			a.errs <- err

			log.Errorf(Category, "Got IPC error, stopping IPC loop")
			return
		}
		go a.dev.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Debugf(Category, "Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Errorf(Category, "Got error, stopping tunnel loop: %s", err)
		return
	case <-a.dev.Wait():
		log.Warnf(Category, "Device wait call, stopping tunnel loop")
		return
	}
}

func (a *TunnelData) setUpInterface() error {
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return err
	}

	return netlink.LinkSetUp(link)
}

func (a *TunnelData) addAddresses() error {
	for _, address := range a.InterfaceConfig.Interface.Addresses {
		if err := a.addAddress(address.String()); err != nil {
			return err
		}
	}

	return nil
}

func (a *TunnelData) addAddress(address string) error {
	log.Debugf(Category, "Adding address %s dev %s", a.InterfaceName, address)

	// sudo ip -4 address add <address> dev <interfaceName>
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("failed finding ip link %s: %w", a.InterfaceName, err)
	}

	addr, err := netlink.ParseAddr(address)
	if err != nil {
		return fmt.Errorf("failed parsing address %s: %w", address, err)
	}

	err = netlink.AddrAdd(link, addr)
	if err != nil {
		return fmt.Errorf("failed adding address %s, %s: %w", link, addr, err)
	}

	return nil
}

func (a *TunnelData) addRoutes() error {
	for _, peer := range a.InterfaceConfig.Peers {
		for _, allowedIP := range peer.AllowedIPs {
			if err := a.addRoute(allowedIP.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *TunnelData) addRoute(address string) error {
	log.Debugf(Category, "Add route for %s %s", a.InterfaceName, address)

	// sudo ip rule add not fwmark <table> table <table>
	ruleNot := netlink.NewRule()
	ruleNot.Invert = true
	ruleNot.Mark = 51820
	ruleNot.Table = 51820
	if err := netlink.RuleAdd(ruleNot); err != nil {
		return fmt.Errorf("failed adding rule 'sudo ip rule add not fwmark <table> table <table>': %w", err)
	}

	// sudo ip rule add table main suppress_prefixlength 0
	ruleAdd := netlink.NewRule()
	ruleAdd.Table = unix.RT_TABLE_MAIN
	ruleAdd.SuppressPrefixlen = 0
	if err := netlink.RuleAdd(ruleAdd); err != nil {
		return fmt.Errorf("failed adding rule 'sudo ip rule add table main suppress_prefixlength 0': %w", err)
	}

	// sudo ip route add <address> dev <interfaceName> table <table>
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("failed finding net link %s: %w", a.InterfaceName, err)
	}

	_, dst, err := net.ParseCIDR(address)
	if err != nil {
		return fmt.Errorf("failed parsing CIDR %s: %w", address, err)
	}

	route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst, Table: 51820}

	if err := netlink.RouteAdd(&route); err != nil {
		return fmt.Errorf("failed adding rule 'routeAdd': %w", err)
	}

	// sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1
	if err := sysctl.Set("net.ipv4.conf.all.src_valid_mark", "1"); err != nil {
		return fmt.Errorf("failed setting sysctl value 'sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1': %w", err)
	}

	return nil
}
