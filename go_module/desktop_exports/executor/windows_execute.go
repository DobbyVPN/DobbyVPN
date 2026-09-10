//go:build windows

package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"go_module/desktop_exports/controlplane"
	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"golang.org/x/sys/windows/svc"
	"google.golang.org/grpc"
)

type managerService struct {
	serverPort int
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
		return nil
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

func (service *managerService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}
	if err := recoverInterruptedState(); err != nil {
		log.Debugf(desktopLogCategory, "[ERROR] failed to recover interrupted product state: %v", err)
		return true, 1
	}

	token, err := controlplane.LoadOrCreateControlToken()
	if err != nil {
		return true, 1
	}
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", service.serverPort))
	if err != nil {
		log.Debugf(desktopLogCategory, "[ERROR] failed to listen: %v", err)
		return true, 1
	}
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			proto.ControlAuthUnaryInterceptor(true, token),
			proto.PanicRecoveryUnaryInterceptor(),
			proto.ErrorLoggingUnaryInterceptor(),
		),
	)

	grpcproto.RegisterVpnServer(grpcServer, &proto.Server{})
	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptSessionChange}

	go func() {
		log.Debugf(desktopLogCategory, "server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Debugf(desktopLogCategory, "[ERROR] failed to serve: %v", err)
		}
	}()

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Stop:
			grpcServer.GracefulStop()
			break loop
		default:
			log.Debugf(desktopLogCategory, "Unexpected service control request #%d", c)
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	return
}

func runService(port int) error {
	return svc.Run("DobbyVPN vpn service", &managerService{serverPort: port})
}

func run(port int) {
	if err := recoverInterruptedState(); err != nil {
		panic(fmt.Sprintf("failed to recover interrupted product state: %v", err))
	}
	token, err := controlplane.LoadOrCreateControlToken()
	if err != nil {
		panic(fmt.Sprintf("failed to prepare control authentication: %v", err))
	}
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		panic(fmt.Sprintf("failed to listen: %v", err))
	}
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			proto.ControlAuthUnaryInterceptor(true, token),
			proto.PanicRecoveryUnaryInterceptor(),
			proto.ErrorLoggingUnaryInterceptor(),
		),
	)

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Debugf(desktopLogCategory, "desktop control listener ready")
	if err := s.Serve(lis); err != nil {
		panic(fmt.Sprintf("failed to serve: %v", err))
	}
}

func (c *Executor) Execute(port int, mode string) {
	if err := initExplicitLocalLog(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialize local logging")
		return
	}
	log.Debugf(desktopLogCategory, "Executing with mode: %v", mode)

	switch mode {
	case "normal":
		run(port)
	case "service":
		runService(port)
	default:
		log.Debugf(desktopLogCategory, "[ERROR] Invalid run mode")
	}
}
