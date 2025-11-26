//go:build windows

package internal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amnezia-vpn/amneziawg-windows-client/manager"
	"github.com/amnezia-vpn/amneziawg-windows-client/ringlogger"
	"github.com/amnezia-vpn/amneziawg-windows/conf"
	"github.com/amnezia-vpn/amneziawg-windows/services"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	ExitSetupSuccess     = 0
	ExitSetupFailed      = 1
	TunnelConfigFolder   = "C:\\ProgramData\\Amnezia\\AmneziaWG"
	TunnelServiceLibPath = "libs\\tunnel-service.exe"
)

type App struct {
	InterfaceName   string
	InterfaceConfig string
}

// NewApp creates a new App that will run on Windows.
func NewApp(interfaceName, awgq_config string) (*App, error) {
	iface := strings.TrimSpace(interfaceName)
	if len(iface) == 0 {
		return nil, fmt.Errorf("interface name is required")
	}

	return &App{
		InterfaceName:   iface,
		InterfaceConfig: awgq_config,
	}, nil
}

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

	serviceAbsolutePath, err := filepath.Abs(TunnelServiceLibPath)
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
		return fmt.Errorf("Failed to start tunnel service %s: %s", "AmneziaWG Tunnel: "+name, err)
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

func saveConfig(configPath, awgq_config string) error {
	err := os.MkdirAll(TunnelConfigFolder, os.ModePerm)
	if err != nil {
		return err
	}

	err = os.WriteFile(configPath, []byte(awgq_config), 0644)
	if err != nil {
		return err
	}

	return nil
}

func deleteConfig(configPath string) error {
	err := os.Remove(configPath)
	if err != nil {
		return err
	}

	return nil
}

func (a *App) Run() error {
	configPath := filepath.Join(TunnelConfigFolder, a.InterfaceName+".conf")
	err := saveConfig(configPath, a.InterfaceConfig)
	if err != nil {
		logrus.Errorf("Failed to save AmneziaWG config: %v", err)
		return err
	} else {
		logrus.Infof("Saved AmneziaWG config")
	}
	err = installTunnel(configPath)
	if err != nil {
		logrus.Errorf("Failed to start tunnel: %v", err)
		return err
	} else {
		logrus.Infof("Started AmneziaWG tunnel")
	}
	return nil
}

func (a *App) Stop() {
	systemConfigPath := filepath.Join(TunnelConfigFolder, a.InterfaceName+".conf")
	err := uninstallTunnel(a.InterfaceName)
	if err != nil {
		logrus.Errorf("Failed to stop tunnel: %v", err)
	} else {
		logrus.Infof("Stopped AmneziaWG tunnel")
	}
	err = deleteConfig(systemConfigPath)
	if err != nil {
		logrus.Errorf("Failed to delete AmneziaWG config: %v", err)
	} else {
		logrus.Infof("Deleted AmneziaWG config")
	}
}
