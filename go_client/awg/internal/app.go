//go:build darwin || linux

package internal

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"

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
	envWgTunFD             = "WG_TUN_FD"
	envWgUapiFD            = "WG_UAPI_FD"
	envWgProcessForeground = "WG_PROCESS_FOREGROUND"
)

type App struct {
	InterfaceName string
	Foreground    bool
	configString  string
	logger        *device.Logger
	tdev          tun.Device
	dev           *device.Device
	uapiListener  net.Listener
	errs          chan error
	term          chan os.Signal
}

// NewApp creates a new App using a config string (e.g. "-f tun0" or "tun0").
func NewApp(config string) (*App, error) {
	parts := splitConfig(config)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty config string")
	}
	var fg bool
	var iface string
	switch parts[0] {
	case "-f", "--foreground":
		fg = true
		if len(parts) < 2 {
			return nil, fmt.Errorf("interface name required with %s flag", parts[0])
		}
		iface = parts[1]
	default:
		iface = parts[0]
	}
	app := &App{
		InterfaceName: iface,
		Foreground:    fg || os.Getenv(envWgProcessForeground) == "1",
		configString:  config,
		errs:          make(chan error, 1),
		term:          make(chan os.Signal, 1),
	}

	logLevel := device.LogLevelError
	switch os.Getenv("LOG_LEVEL") {
	case "verbose", "debug":
		logLevel = device.LogLevelVerbose
	case "error":
		logLevel = device.LogLevelError
	case "silent":
		logLevel = device.LogLevelSilent
	}
	app.logger = device.NewLogger(logLevel, fmt.Sprintf("(%s) ", app.InterfaceName))
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
	uapi, err := ipc.UAPIListen(a.InterfaceName, fileUAPI)
	if err != nil {
		a.logger.Errorf("UAPI listen error: %v", err)
		return err
	}
	a.uapiListener = uapi

	a.dev = device.NewDevice(a.tdev, conn.NewDefaultBind(), a.logger)
	go a.acceptLoop()

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

func (a *App) Stop() {
	if a.uapiListener != nil {
		_ = a.uapiListener.Close()
	}
	if a.dev != nil {
		a.dev.Close()
	}
	a.logger.Verbosef("Shutting down")
}

func splitConfig(config string) []string {
	return strings.Fields(config)
}

func (a *App) openTun() (tun.Device, error) {
	if fdStr := os.Getenv(envWgTunFD); fdStr != "" {
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
	if fdStr := os.Getenv(envWgUapiFD); fdStr != "" {
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
