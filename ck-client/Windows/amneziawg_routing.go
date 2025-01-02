//go:build windows
// +build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/amnezia-vpn/amneziawg-windows-client/manager"
	"github.com/amnezia-vpn/amneziawg-windows-client/ringlogger"
	"github.com/amnezia-vpn/amneziawg-windows/conf"
	"github.com/amnezia-vpn/amneziawg-windows/services"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const TUNNEL_SERVICE_LIB_PATH = "libs\\tunnel-service.exe"

// Runs tunnel service and confgures it using configuration file provided via its path
func installTunnel(configPath string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to service manager: %s", err)
	}

	name, err := conf.NameFromPath(configPath)
	if err != nil {
		return fmt.Errorf("Filed to get tunnel name by config file path: %s", err)
	}

	serviceName, err := services.ServiceNameOfTunnel(name)
	if err != nil {
		return fmt.Errorf("Filed to get tunnel service name by tunnel name: %s", err)
	}

	service, err := m.OpenService(serviceName)
	if err == nil {
		status, err := service.Query()
		if err != nil && err != windows.ERROR_SERVICE_MARKED_FOR_DELETE {
			service.Close()
			return fmt.Errorf("Failed to query tunnel service status: %s", err)
		}
		if status.State != svc.Stopped && err != windows.ERROR_SERVICE_MARKED_FOR_DELETE {
			service.Close()
			return fmt.Errorf("Tunnel already installed and running")
		}
		err = service.Delete()
		service.Close()
		if err != nil && err != windows.ERROR_SERVICE_MARKED_FOR_DELETE {
			return fmt.Errorf("Failed to close tunnel service: %s", err)
		}

		for {
			service, err = m.OpenService(serviceName)
			if err != nil && err != windows.ERROR_SERVICE_MARKED_FOR_DELETE {
				break
			}
			service.Close()
			time.Sleep(time.Second / 3)
		}
	}

	config := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		Dependencies: []string{"Nsi", "TcpIp"},
		DisplayName:  "AmneziaWG Tunnel: " + name,
		SidType:      windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}

	serviceAbsolutePath, err := filepath.Abs(TUNNEL_SERVICE_LIB_PATH)
	if err != nil {
		return fmt.Errorf("Filed to get tunnel service absolute path: %s", err)
	}

	service, err = m.CreateService(serviceName, serviceAbsolutePath, config, configPath)
	if err != nil {
		return fmt.Errorf("Failed to create tunnel service: %s", err)
	}

	err = service.Start()
	if err != nil {
		service.Delete()
		return fmt.Errorf("Failed to start tunnel service: %s", err)
	}

	return err
}

func dumpLog(logFilePath string, continious bool) error {
	file, err := os.OpenFile(logFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0666)
	if err != nil {
		return err
	}
	defer file.Close()

	logPath, err := manager.LogFile(false)
	if err != nil {
		return err
	}

	return ringlogger.DumpTo(logPath, file, continious)
}

// Removed tunnel service by name
func uninstallTunnel(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("Failed to connect to service manager: %s", err)
	}

	serviceName, err := services.ServiceNameOfTunnel(name)
	if err != nil {
		return fmt.Errorf("Failed to get service name: %s", err)
	}

	service, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("Failed to connect to open service: %s", err)
	}

	service.Control(svc.Stop)
	err = service.Delete()
	err2 := service.Close()
	if err != nil && err != windows.ERROR_SERVICE_MARKED_FOR_DELETE {
		return fmt.Errorf("Failed to close service: %s", err)
	}

	return err2
}
