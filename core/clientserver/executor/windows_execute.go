//go:build windows

package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"core/clientserver/controljson"
	"core/clientserver/controlplane"
	"core/sessionapi"
	"core/tunnel/platform_engine"

	"core/log"

	"golang.org/x/sys/windows/svc"
)

type managerService struct{ initLog func() error }

func prepareDesktopControl() (net.Listener, error) {
	return prepareDesktopControlWith(
		func() error { return platform_engine.RecoverStaleWindowsNRPTRule(2 * time.Second) },
		controlplane.ListenDesktopControlPipe,
		func() error {
			startupFailure = prepareServiceProtection()
			if startupFailure != nil {
				return nil
			} // Open the pipe so the operator sees the retained policy failure.
			return recoverInterruptedState()
		},
		platform_engine.RecoverStaleWindowsIPv6FirewallRules,
	)
}

func prepareDesktopControlWith(
	recoverNRPT func() error,
	listen func() (net.Listener, error),
	recoverInterrupted func() error,
	recoverStaleIPv6Rules func() error,
) (net.Listener, error) {
	// A stale NRPT rule sends all of the machine's DNS to the tunnel servers.
	// Remove it first, whatever else fails at startup. A failure is logged, not
	// fatal: connect and disconnect retry the removal.
	if err := recoverNRPT(); err != nil {
		log.Errorf(desktopLogCategory, "stale NRPT rule recovery failed: %v", err)
	}
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
	startRestore := prepareDesktopRestore(desktopProcessBinding(), releaseUnadoptedKillSwitch)
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- controljson.Serve(listener, controljson.Handler{Binding: desktopProcessBinding()}, nil)
	}()
	startRestore()
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

func prepareLocalLogPath() (string, error) {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	if requested == "" {
		programData := strings.TrimSpace(os.Getenv("ProgramData"))
		if programData == "" {
			return "", fmt.Errorf("ProgramData is unavailable for Go backend logs")
		}
		requested = filepath.Join(programData, "DobbyVPN", "Logs", "backend.jsonl")
	} else {
		root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
		if root == "" {
			root = os.TempDir()
		}
		var err error
		requested, err = secureExplicitLogPath(root, requested)
		if err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(requested), 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(requested)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return "", fmt.Errorf("explicit log target must be a regular file")
		}
	} else if !os.IsNotExist(err) || strings.TrimSpace(os.Getenv("DOBBY_LOG_PRECREATED")) == "1" {
		return "", err
	}
	return requested, nil
}

func (service *managerService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}
	if err := service.initLog(); err != nil {
		reportStartupFailure("initialize local logging", err, true)
		return true, 1
	}
	log.Infof(desktopLogCategory, "Windows service dispatcher connected, preparing desktop control")
	listener, err := prepareDesktopControl()
	if err != nil {
		log.Errorf(desktopLogCategory, "failed to prepare desktop control: %v", err)
		return true, 1
	}
	stopControl, serveDone := serveDesktopControl(listener)
	emit := newNetworkEvents()
	if stopWatching, err := watchNetworkChanges(emit); err != nil {
		log.Errorf(desktopLogCategory, "network change watcher unavailable: %v", err)
	} else {
		defer stopWatching()
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPreShutdown | svc.AcceptPowerEvent}
	var serveErr error
running:
	for {
		select {
		case serveErr = <-serveDone:
			break running
		case request, ok := <-requests:
			if !ok || request.Cmd == svc.Stop || request.Cmd == svc.Shutdown || request.Cmd == svc.PreShutdown {
				break running
			}
			if request.Cmd == svc.Interrogate {
				changes <- request.CurrentStatus
			}
			if request.Cmd == svc.PowerEvent {
				powerEvent(request.EventType, emit)
			}
		}
	}
	changes <- svc.Status{State: svc.StopPending, WaitHint: uint32(sessionapi.CleanupTimeout / time.Millisecond)}
	if err := shutdownDesktop(stopControl, serveErr, desktopProcessBinding()); err != nil {
		log.Errorf(desktopLogCategory, "desktop shutdown failed: %v", err)
		return true, 1
	}
	return false, 0
}

func runService() error {
	return svc.Run("DobbyVPN Go backend", &managerService{initLog: initExplicitLocalLog})
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
	if err := shutdownDesktop(stopControl, serveErr, desktopProcessBinding()); err != nil {
		panic(fmt.Sprintf("desktop shutdown failed: %v", err))
	}
}

func (c *Executor) Execute(mode string) {
	if code := executeWith(mode, initExplicitLocalLog, run, runService); code != 0 {
		os.Exit(code)
	}
}

// executeWith returns the process exit code. Only failures before or outside
// the SCM handler are non-zero.
func executeWith(mode string, initLog func() error, runNormal func(), runServiceMode func() error) int {
	if mode == "service" {
		// SCM waits for the dispatcher while starting automatic services.
		// Configuration belongs to the installer, logging to the registered handler.
		if err := runServiceMode(); err != nil {
			return reportStartupFailure("run service dispatcher", err, true)
		}
		return 0
	}
	if err := initLog(); err != nil {
		return reportStartupFailure("initialize local logging", err, false)
	}
	log.Debugf(desktopLogCategory, "Executing with mode: %v", mode)

	switch mode {
	case "release-protection":
		err := removeDesktopInstallationState(context.Background(), controlplane.WindowsInstallationSourceStore)
		// Uninstall must not leave the machine's DNS pointed at the tunnel servers.
		// The service is stopped by now; wait a little for its owner mutex.
		err = errors.Join(err, platform_engine.RecoverStaleWindowsNRPTRule(10*time.Second))
		if err != nil {
			log.Errorf(desktopLogCategory, "failed to release installed VPN state: %v", err)
			return reportStartupFailure("release installed VPN state", err, true)
		}
	case "normal":
		runNormal()
	default:
		log.Errorf(desktopLogCategory, "Invalid run mode")
		return reportStartupFailure("select run mode", fmt.Errorf("invalid run mode %q", mode), false)
	}
	return 0
}
