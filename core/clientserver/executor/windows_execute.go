//go:build windows

package executor

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"core/clientserver/controljson"
	"core/clientserver/controlplane"
	"core/tunnel/platform_engine"

	"core/log"

	"golang.org/x/sys/windows/svc"
)

type managerService struct{}

func prepareDesktopControl() (net.Listener, error) {
	return prepareDesktopControlWith(
		controlplane.ListenDesktopControlPipe,
		recoverInterruptedState,
		platform_engine.RecoverStaleWindowsIPv6FirewallRules,
	)
}

func prepareDesktopControlWith(
	listen func() (net.Listener, error),
	recoverInterrupted func() error,
	recoverStaleIPv6Rules func() error,
) (net.Listener, error) {
	listener, err := listen()
	if err != nil {
		return nil, fmt.Errorf("listen for desktop control: %w", err)
	}
	if err := recoverInterrupted(); err != nil {
		return nil, closePipeAfterStartupFailure(listener, fmt.Errorf("recover interrupted product state: %w", err))
	}
	if err := recoverStaleIPv6Rules(); err != nil {
		return nil, closePipeAfterStartupFailure(listener, fmt.Errorf("recover stale Windows IPv6 firewall rules: %w", err))
	}
	return listener, nil
}

func closePipeAfterStartupFailure(listener net.Listener, startupErr error) error {
	if err := listener.Close(); err != nil {
		return errors.Join(startupErr, fmt.Errorf("close desktop control pipe after startup failure: %w", err))
	}
	return startupErr
}

func serveDesktopControl(listener net.Listener) (func() error, <-chan error) {
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- controljson.Serve(listener, controljson.Handler{Binding: desktopProcessBinding()}, nil)
	}()
	return listener.Close, serveDone
}

func secureExplicitLogPath(root, requested string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	requested, err = filepath.Abs(requested)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, requested)
	if err != nil {
		return "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("explicit log path is outside the local temporary directory")
	}
	return requested, nil
}

func openPrecreatedAppendLog(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return nil, err
	}
	return file, nil
}

func initExplicitLocalLog() error {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	if requested == "" {
		programData := strings.TrimSpace(os.Getenv("ProgramData"))
		if programData == "" {
			return fmt.Errorf("ProgramData is unavailable for Go backend logs")
		}
		return log.SetPath(filepath.Join(programData, "DobbyVPN", "Logs", "backend.jsonl"))
	}
	root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
	if root == "" {
		root = os.TempDir()
	}
	path, err := secureExplicitLogPath(root, requested)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("DOBBY_LOG_PRECREATED")) == "1" {
		if info, statErr := os.Lstat(path); statErr != nil {
			return statErr
		} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("explicit log target must be a regular file")
		}
		file, openErr := openPrecreatedAppendLog(path)
		if openErr != nil {
			return openErr
		}
		return log.SetOpenedFile(file)
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("explicit log target must be a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	return log.SetPath(path)
}

func (service *managerService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}
	listener, err := prepareDesktopControl()
	if err != nil {
		log.Debugf(desktopLogCategory, "[ERROR] failed to prepare desktop control: %v", err)
		return true, 1
	}
	stopControl, serveDone := serveDesktopControl(listener)
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	var serveErr error
running:
	for {
		select {
		case serveErr = <-serveDone:
			break running
		case request, ok := <-requests:
			if !ok || request.Cmd == svc.Stop || request.Cmd == svc.Shutdown {
				break running
			}
			if request.Cmd == svc.Interrogate {
				changes <- request.CurrentStatus
			}
		}
	}
	changes <- svc.Status{State: svc.StopPending, WaitHint: 30000}
	if err := shutdownDesktop(stopControl, serveErr); err != nil {
		log.Errorf(desktopLogCategory, "desktop shutdown failed: %v", err)
		return true, 1
	}
	return false, 0
}

func runService() error {
	return svc.Run("DobbyVPN Go backend", &managerService{})
}

func run() {
	listener, err := prepareDesktopControl()
	if err != nil {
		panic(fmt.Sprintf("failed to prepare desktop control: %v", err))
	}
	stopControl, serveDone := serveDesktopControl(listener)
	log.Debugf(desktopLogCategory, "desktop JSON control pipe ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	var serveErr error
	select {
	case <-signals:
	case serveErr = <-serveDone:
	}
	if err := shutdownDesktop(stopControl, serveErr); err != nil {
		panic(fmt.Sprintf("desktop shutdown failed: %v", err))
	}
}

func (c *Executor) Execute(mode string) {
	if err := initExplicitLocalLog(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialize local logging")
		return
	}
	log.Debugf(desktopLogCategory, "Executing with mode: %v", mode)

	switch mode {
	case "normal":
		run()
	case "service":
		if err := runService(); err != nil {
			log.Debugf(desktopLogCategory, "[ERROR] Go backend service failed: %v", err)
		}
	default:
		log.Debugf(desktopLogCategory, "[ERROR] Invalid run mode")
	}
}
