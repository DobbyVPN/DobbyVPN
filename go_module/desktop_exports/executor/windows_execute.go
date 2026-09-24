//go:build windows

package executor

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"go_module/desktop_exports/controljson"
	"go_module/desktop_exports/controlplane"

	"go_module/log"

	"golang.org/x/sys/windows/svc"
)

type managerService struct{}

func serveDesktopControl() (func() error, <-chan error, error) {
	listener, err := controlplane.ListenDesktopControlPipe()
	if err != nil {
		return nil, nil, err
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- controljson.Serve(listener, controljson.Handler{Binding: desktopProcessBinding()}, nil)
	}()
	return listener.Close, serveDone, nil
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
	if err := recoverInterruptedState(); err != nil {
		log.Debugf(desktopLogCategory, "[ERROR] failed to recover interrupted product state: %v", err)
		return true, 1
	}
	stopControl, serveDone, err := serveDesktopControl()
	if err != nil {
		log.Debugf(desktopLogCategory, "[ERROR] failed to listen for desktop control: %v", err)
		return true, 1
	}
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptSessionChange}
	for request := range requests {
		if request.Cmd != svc.Stop {
			log.Debugf(desktopLogCategory, "Unexpected service control request #%d", request.Cmd)
			continue
		}
		if err := stopControl(); err != nil {
			log.Debugf(desktopLogCategory, "[ERROR] failed to close desktop control: %v", err)
			return true, 1
		}
		if err := <-serveDone; err != nil {
			log.Debugf(desktopLogCategory, "[ERROR] desktop control stopped with error: %v", err)
			return true, 1
		}
		changes <- svc.Status{State: svc.StopPending}
		return false, 0
	}
	return false, 0
}

func runService() error {
	return svc.Run("DobbyVPN Go backend", &managerService{})
}

func run() {
	if err := recoverInterruptedState(); err != nil {
		panic(fmt.Sprintf("failed to recover interrupted product state: %v", err))
	}
	stopControl, serveDone, err := serveDesktopControl()
	if err != nil {
		panic(fmt.Sprintf("failed to listen for desktop control: %v", err))
	}
	log.Debugf(desktopLogCategory, "desktop JSON control pipe ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	if err := stopControl(); err != nil {
		panic(fmt.Sprintf("failed to close desktop control pipe: %v", err))
	}
	if err := <-serveDone; err != nil {
		panic(fmt.Sprintf("desktop control stopped with error: %v", err))
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
