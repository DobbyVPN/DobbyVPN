//go:build windows

package executor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

type Executor struct{}

func empty() error {
	return nil
}

func runService(executable string) (func() error, error) {
	config := mgr.Config{
		ServiceType:  windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		Dependencies: []string{"Nsi", "TcpIp"},
		DisplayName:  "DobbyVPN vpn service",
		SidType:      windows.SERVICE_SID_TYPE_UNRESTRICTED,
	}

	log.Printf("Connecting to windows service manager\n")
	m, err := mgr.Connect()
	if err != nil {
		return empty, fmt.Errorf("Cannot connect to Windows Service manager: %v", err)
	}

	log.Printf("Creating windows service\n")
	service, err := m.CreateService("DobbyVPN vpn service", executable, config, "-mode=service")
	if err != nil {
		return empty, fmt.Errorf("Cannot create service: %v", err)
	}

	log.Printf("Starting windows service\n")
	err = service.Start()
	if err != nil {
		return empty, fmt.Errorf("Cannot start service: %v", err)
	}

	stopService := func() error {
		log.Printf("Stopping service")
		service.Control(svc.Stop)
		err = service.Delete()
		if err != nil {
			log.Printf("WARNING: Cannot delete service: %v", err)
		}
		err2 := service.Close()
		if err2 != nil {
			log.Printf("WARNING: Cannot close service: %v", err2)
		}

		if err != nil {
			return err
		}
		if err2 != nil {
			return err2
		}
		return nil
	}

	return stopService, nil
}

func run(executable string) (func() error, error) {
	tmpFile, err := os.CreateTemp("./", "vpnserver-output-*.log")
	if err != nil {
		return empty, fmt.Errorf("Error creating vpn subprocess temporal log file: %v", err)
	}
	defer tmpFile.Close()

	path, err := filepath.Abs(tmpFile.Name())
	if err != nil {
		return empty, fmt.Errorf("Error printing temporal file absolute path: %v", err)
	}
	log.Printf("Created temp log file: %v\n", path)

	cmd := exec.Command(executable)
	cmd.Stdout = tmpFile
	cmd.Stderr = tmpFile

	if err := cmd.Start(); err != nil {
		return empty, fmt.Errorf("Failed to start vpn subprocess: %v\n", err)
	}

	stop := func() error {
		log.Println("Interrupting subprocess...")
		cmd.Process.Kill()

		err := cmd.Wait()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				log.Printf("Subprocess exited with code: %d\n", exitErr.ExitCode())
			} else {
				log.Printf("Wait error: %v\n", err)

				return err
			}
		}

		return nil
	}

	return stop, nil
}

func (e *Executor) Execute(executable, mode string) (func() error, error) {
	switch mode {
	case "normal":
		return run(executable)
	case "service":
		return runService(executable)
	default:
		return empty, fmt.Errorf("Unexpected mode: %v", mode)
	}
}
