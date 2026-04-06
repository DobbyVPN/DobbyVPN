//go:build linux

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
	logger          *device.Logger
	dev             *device.Device
	uapi            net.Listener
	errs            chan error
}

func CreateTunnelData(tun string, conf *config.Config) *TunnelData {
	return &TunnelData{
		InterfaceName:   tun,
		InterfaceConfig: conf,
	}
}

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

const (
	ENV_WG_TUN_FD             = "WG_TUN_FD"
	ENV_WG_UAPI_FD            = "WG_UAPI_FD"
	ENV_WG_PROCESS_FOREGROUND = "WG_PROCESS_FOREGROUND"
)

func (a *TunnelData) Run() error {
	var err error

	log.Infof("[AWG] Running awg tunnel (linux)")
	a.errs = make(chan error, 1)

	log.Infof("[AWG] DeduplicateNetworkEntries")
	a.InterfaceConfig.DeduplicateNetworkEntries()

	log.Infof("[AWG] Converting interface config to the UAPI config")
	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		return fmt.Errorf("Failed to convert config to UAPI: %s", err)
	}
	log.Infof("[AWG] [UAPI] %s", uapiConf)

	log.Infof("[AWG] Create awg TUN device")
	tdev, err := tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
	if err != nil {
		return fmt.Errorf("Failed to create TUN device: %s", err)
	}

	log.Infof("[AWG] Creating interface instance")
	bind := conn.NewDefaultBind()
	a.dev = device.NewDevice(tdev, bind, &device.Logger{log.Infof, log.Infof})

	log.Infof("[AWG] Setting interface configuration")
	fileUAPI, err := ipc.UAPIOpen(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("Failed to open UAPI file: %s", err)
	}
	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		return fmt.Errorf("UAPI listen error: %v", err)
	}
	a.uapi = uapi

	log.Infof("[AWG] Seting up UAPI config")
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		return fmt.Errorf("IPC set error: %v", err)
	}

	log.Infof("[AWG] Bringing peers up")
	err = a.dev.Up()
	if err != nil {
		return fmt.Errorf("Bringing peers up error: %v", err)
	}

	log.Infof("[AWG] Setting up linux subnet")

	log.Infof("[AWG] Setting up %s interface", a.InterfaceName)
	if err = a.setUpInterface(); err != nil {
		return err
	}

	log.Infof("[AWG] Adding all addresses")
	if err = a.addAddresses(); err != nil {
		return err
	}

	log.Infof("[AWG] Adding all routes")
	if err = a.addRoutes(); err != nil {
		return err
	}

	log.Infof("[AWG] IPC accept loop")
	go a.ipcAcceptLoop()

	log.Infof("[AWG] Tunnel loop")
	go a.tunnelLoop()

	return nil
}

func (a *TunnelData) Stop() {
	log.Infof("[AWG] Shutting down")

	if a.uapi != nil {
		a.uapi.Close()
	}
	if a.dev != nil {
		a.dev.Close()
	}
}

func (a *TunnelData) ipcAcceptLoop() {
	log.Infof("[AWG] Running IPC accept loop")

	for {
		c, err := a.uapi.Accept()
		if err != nil {
			a.errs <- err

			log.Infof("[AWG] [ERROR] Got IPC error, stopping IPC loop")
			return
		}
		go a.dev.IpcHandle(c)
	}
}

func (a *TunnelData) tunnelLoop() {
	log.Infof("[AWG] Running tunnel loop")

	defer a.Stop()

	select {
	case err := <-a.errs:
		log.Infof("[AWG] [ERROR] Got error, stopping tunnel loop: %s", err)
		return
	case <-a.dev.Wait():
		log.Infof("[AWG] [WARNING] Device wait call, stopping tunnel loop")
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
	log.Infof("[AWG] Adding address %s dev %s", a.InterfaceName, address)

	// sudo ip -4 address add <address> dev <interfaceName>
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("Error finding ip link %s: %s", a.InterfaceName, err)
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

func (a *TunnelData) addRoutes() error {
	for _, peer := range a.InterfaceConfig.Peers {
		for _, allowed_ip := range peer.AllowedIPs {
			if err := a.addRoute(allowed_ip.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *TunnelData) addRoute(address string) error {
	log.Infof("[AWG] Add route for %s %s", a.InterfaceName, address)

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
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return fmt.Errorf("Error finding net link %s: %s", a.InterfaceName, err)
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
