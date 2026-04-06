//go:build windows

package internal

import (
	"fmt"
	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/windows"
	"net"
	"os"
	"os/signal"
	"strings"
)

const (
	ExitSetupSuccess = 0
	ExitSetupFailed  = 1
)

type App struct {
	InterfaceName string
	logger        *device.Logger
	tdev          tun.Device
	dev           *device.Device
	uapiListener  net.Listener
	errs          chan error
	term          chan os.Signal
}

// NewApp creates a new App that will run on Windows. It expects exactly one argument: the interface name (e.g. "tun0").
func NewApp(config string) (*App, error) {
	iface := strings.TrimSpace(config)
	if len(iface) == 0 {
		return nil, fmt.Errorf("interface name is required")
	}

	logLevel := device.LogLevelVerbose
	logger := device.NewLogger(logLevel, fmt.Sprintf("(%s) ", iface))

	return &App{
		InterfaceName: iface,
		logger:        logger,
		errs:          make(chan error, 1),
		term:          make(chan os.Signal, 1),
	}, nil
}

func (a *App) Run() error {
	tdev, err := tun.CreateTUN(a.InterfaceName, 0)
	if err != nil {
		a.logger.Errorf("Failed to create TUN device: %v", err)
		return err
	}
	a.tdev = tdev

	if realIface, err := tdev.Name(); err == nil {
		a.InterfaceName = realIface
	}

	a.dev = device.NewDevice(a.tdev, conn.NewDefaultBind(), a.logger)
	if err := a.dev.Up(); err != nil {
		a.logger.Errorf("Failed to bring up device: %v", err)
		return err
	}
	a.logger.Verbosef("Device started")

	uapi, err := ipc.UAPIListen(a.InterfaceName)
	if err != nil {
		a.logger.Errorf("Failed to listen on UAPI socket: %v", err)
		return err
	}
	a.uapiListener = uapi

	go a.acceptLoop()
	a.logger.Verbosef("UAPI listener started")

	signal.Notify(a.term, os.Interrupt, os.Kill, windows.SIGTERM)

	select {
	case <-a.term:
	case err := <-a.errs:
		return err
	case <-a.dev.Wait():
	}

	a.Stop()
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

func (a *App) acceptLoop() {
	for {
		conn, err := a.uapiListener.Accept()
		if err != nil {
			a.errs <- err
			return
		}
		go a.dev.IpcHandle(conn)
	}
}
