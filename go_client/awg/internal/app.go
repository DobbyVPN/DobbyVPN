//go:build darwin || linux

package internal

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"time"

	sysctl "github.com/lorenzosaino/go-sysctl"
	"github.com/vishvananda/netlink"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

const (
	ENV_WG_TUN_FD             = "WG_TUN_FD"
	ENV_WG_UAPI_FD            = "WG_UAPI_FD"
	ENV_WG_PROCESS_FOREGROUND = "WG_PROCESS_FOREGROUND"
)

type App struct {
	InterfaceName   string
	InterfaceConfig *Config
	logger          *device.Logger
	tdev            tun.Device
	dev             *device.Device
	uapiListener    net.Listener
	errs            chan error
	term            chan os.Signal
}

// NewApp creates a new App using a tunnel name and its config
func NewApp(tunnel, config string) (*App, error) {
	awgqconfig, err := FromWgQuickWithUnknownEncoding(config, tunnel)
	if err != nil {
		return nil, err
	}

	app := &App{
		InterfaceName:   tunnel,
		InterfaceConfig: awgqconfig,
		errs:            make(chan error, 1),
		term:            make(chan os.Signal, 1),
	}

	app.logger = device.NewLogger(device.LogLevelVerbose, fmt.Sprintf("(%s) ", app.InterfaceName))
	return app, nil
}

func (a *App) Run() error {
	tdev, err := a.openTun()
	if err != nil {
		a.logger.Errorf("TUN error: %v", err)
		return err
	}
	a.tdev = tdev

	fileUAPI, err := a.openUAPI()
	if err != nil {
		a.logger.Errorf("UAPI error: %v", err)
		return err
	}

	uapiConf, err := a.InterfaceConfig.ToUAPI()
	if err != nil {
		a.logger.Errorf("Failed to convert config to UAPI")
		return err
	} else {
		a.logger.Verbosef("[UAPI] %s", uapiConf)
	}

	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		a.logger.Errorf("UAPI listen error: %v", err)
		return err
	}

	a.uapiListener = uapi

	a.dev = device.NewDevice(a.tdev, conn.NewDefaultBind(), a.logger)
	err = a.dev.IpcSet(uapiConf)
	if err != nil {
		a.logger.Errorf("IPC set error: %v", err)
		return err
	}

	go a.acceptLoop()
	go a.setupNet()

	signal.Notify(a.term, unix.SIGTERM, os.Interrupt)

	select {
	case <-a.term:
	case err := <-a.errs:
		return err
	case <-a.dev.Wait():
	}

	a.Stop()
	return nil
}

func (a *App) setUpInterface() error {
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return err
	}

	return netlink.LinkSetUp(link)
}

func (a *App) addAddresses() error {
	for _, address := range a.InterfaceConfig.Interface.Addresses {
		if err := a.addAddress(address.String()); err != nil {
			return err
		}
	}

	return nil
}

func (a *App) addAddress(address string) error {
	a.logger.Verbosef("Add address %s %s", a.InterfaceName, address)
	// sudo ip -4 address add <address> dev <interfaceName>
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return err
	}

	addr, err := netlink.ParseAddr(address)
	if err != nil {
		return err
	}

	return netlink.AddrAdd(link, addr)
}

func (a *App) addRoutes() error {
	for _, peer := range a.InterfaceConfig.Peers {
		for _, allowed_ip := range peer.AllowedIPs {
			if err := a.addRoute(allowed_ip.String()); err != nil {
				return err
			}
		}
	}

	return nil
}

func (a *App) addRoute(address string) error {
	a.logger.Verbosef("Add route for %s %s", a.InterfaceName, address)
	// sudo ip rule add not fwmark <table> table <table>
	ruleNot := netlink.NewRule()
	ruleNot.Invert = true
	ruleNot.Mark = 51820
	ruleNot.Table = 51820
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
	link, err := netlink.LinkByName(a.InterfaceName)
	if err != nil {
		return err
	}

	_, dst, err := net.ParseCIDR(address)
	if err != nil {
		return err
	}

	route := netlink.Route{LinkIndex: link.Attrs().Index, Dst: dst, Table: 51820}

	if err := netlink.RouteAdd(&route); err != nil {
		return err
	}

	// sudo sysctl -q net.ipv4.conf.all.src_valid_mark=1
	if err := sysctl.Set("net.ipv4.conf.all.src_valid_mark", "1"); err != nil {
		return err
	}

	return nil
}

func (a *App) setupNet() error {
	var err error
	// Wait for service to run
	time.Sleep(100 * time.Millisecond)

	if err = a.setUpInterface(); err != nil {
		a.logger.Errorf("Interface set up error: %v", err)
		return err
	} else {
		a.logger.Verbosef("Interface set up success")
	}

	if a.addAddresses(); err != nil {
		a.logger.Errorf("Interface addresses addition error: %v", err)
		return err
	} else {
		a.logger.Verbosef("Interface addresses addition success")
	}

	if a.addRoutes(); err != nil {
		a.logger.Errorf("Interface routing initialisation error: %v", err)
		return err
	} else {
		a.logger.Verbosef("Interface routing initialisation success")
	}

	return nil
}

func (a *App) Stop() {
	if a.uapiListener != nil {
		a.uapiListener.Close()
	}
	if a.dev != nil {
		a.dev.Close()
	}
	a.logger.Verbosef("Shutting down")
}

func (a *App) openTun() (tun.Device, error) {
	if fdStr := os.Getenv(ENV_WG_TUN_FD); fdStr != "" {
		fd, err := strconv.ParseUint(fdStr, 10, 32)
		if err != nil {
			return nil, err
		}
		if err := unix.SetNonblock(int(fd), true); err != nil {
			return nil, err
		}
		file := os.NewFile(uintptr(fd), "")
		return tun.CreateTUNFromFile(file, device.DefaultMTU)
	}
	return tun.CreateTUN(a.InterfaceName, device.DefaultMTU)
}

func (a *App) openUAPI() (*os.File, error) {
	if fdStr := os.Getenv(ENV_WG_UAPI_FD); fdStr != "" {
		fd, err := strconv.ParseUint(fdStr, 10, 32)
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), ""), nil
	}
	return ipc.UAPIOpen(a.InterfaceName)
}

func (a *App) acceptLoop() {
	for {
		c, err := a.uapiListener.Accept()
		if err != nil {
			a.errs <- err
			return
		}
		go a.dev.IpcHandle(c)
	}
}
