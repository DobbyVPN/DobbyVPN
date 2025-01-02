package main

import (
	"fmt"
	"os"
	"os/signal"

	"github.com/amnezia-vpn/amneziawg-go/conn"
	"github.com/amnezia-vpn/amneziawg-go/device"
	"github.com/amnezia-vpn/amneziawg-go/ipc"
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

func main() {
	if len(os.Args) != 4 {
		os.Exit(1)
	}

	var interfaceName string = os.Args[1]
	var configFilePath string = os.Args[2]
	var logLevelArg string = os.Args[3]

	var logLevel int

	// Get log level
	switch logLevelArg {
	case "verbose", "debug":
		logLevel = device.LogLevelVerbose
	case "error":
		logLevel = device.LogLevelError
	case "silent":
		logLevel = device.LogLevelSilent
	default:
		logLevel = device.LogLevelError
	}

	// open TUN device (or use supplied fd)
	tdev, err := tun.CreateTUN(interfaceName, device.DefaultMTU)

	if err == nil {
		realInterfaceName, err2 := tdev.Name()
		if err2 == nil {
			interfaceName = realInterfaceName
		}
	}

	logger := device.NewLogger(
		logLevel,
		fmt.Sprintf("(%s) ", interfaceName),
	)

	if err != nil {
		logger.Errorf("Failed to create TUN device: %v", err)
		os.Exit(1)
	}

	// open UAPI file (or use supplied fd)
	fileUAPI, err := ipc.UAPIOpen(interfaceName)

	if err != nil {
		logger.Errorf("UAPI listen error: %v", err)

		os.Exit(1)
	}

	// Device init
	device := device.NewDevice(tdev, conn.NewDefaultBind(), logger)

	logger.Verbosef("Device started")

	errs := make(chan error)
	term := make(chan os.Signal, 1)

	// UAPI init
	uapi, err := ipc.UAPIListen(interfaceName, fileUAPI)

	if err != nil {
		logger.Errorf("Failed to listen on uapi socket: %v", err)
		os.Exit(1)
	}

	// Configure:
	configData, err := os.ReadFile(configFilePath)
	if err != nil {
		logger.Errorf("Failed to read config file: %s", err)
		os.Exit(1)
	}

	config, err := FromWgQuickWithUnknownEncoding(string(configData), interfaceName)
	if err != nil {
		logger.Errorf("Failed to process config: %s", err)
		os.Exit(1)
	}

	uapiString, err := config.ToUAPI()
	if err != nil {
		logger.Errorf("Failed to process config: %s", err)
		os.Exit(1)
	}

	logger.Verbosef("[UAPI] %s", &uapiString)

	device.IpcSet(uapiString)

	// Listen for uapi requests
	go func() {
		for {
			conn, err := uapi.Accept()
			if err != nil {
				errs <- err
				return
			}
			go device.IpcHandle(conn)
		}
	}()

	logger.Verbosef("UAPI listener started")

	// wait for program to terminate

	signal.Notify(term, unix.SIGTERM)
	signal.Notify(term, os.Interrupt)

	select {
	case <-term:
	case <-errs:
	case <-device.Wait():
	}

	// clean up

	uapi.Close()
	device.Close()

	logger.Verbosef("Shutting down")
}
