//go:build windows

package executor

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"go_module/core/common"
	"go_module/desktop_exports/controlplane"
	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"golang.org/x/sys/windows"
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

func rejectReparseTraversal(root, target string) error {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("explicit log path is outside the local temporary directory")
	}
	current := root
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err != nil {
			if err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_PATH_NOT_FOUND {
				continue
			}
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("explicit log path traverses a reparse point")
		}
	}
	return nil
}

func initExplicitLocalLog() error {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	if requested == "" {
		return nil
	}
	path, err := secureExplicitLogPath(os.TempDir(), requested)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := rejectReparseTraversal(os.TempDir(), parent); err != nil {
		return err
	}

	resolvedRoot, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	path, err = secureExplicitLogPath(
		resolvedRoot,
		filepath.Join(resolvedParent, filepath.Base(path)),
	)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("explicit log target must be a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := rejectReparseTraversal(resolvedRoot, resolvedParent); err != nil {
		return err
	}
	if err := controlplane.SecureExplicitUserPath(resolvedParent); err != nil {
		return err
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(resolvedParent); err != nil {
		return err
	}
	if err := log.SetPath(path); err != nil {
		return err
	}
	if err := rejectReparseTraversal(resolvedRoot, path); err != nil {
		return err
	}
	if err := controlplane.SecureExplicitUserPath(path); err != nil {
		return err
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(path); err != nil {
		return err
	}
	return nil
}

func (service *managerService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (svcSpecificEC bool, exitCode uint32) {
	changes <- svc.Status{State: svc.StartPending}

	token, err := controlplane.LoadOrCreateControlToken()
	if err != nil {
		return true, 1
	}
	lis, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", service.serverPort))
	if err != nil {
		log.Debugf(common.Category, "[ERROR] failed to listen: %v", err)
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
		log.Debugf(common.Category, "server listening at %v", lis.Addr())
		if err := grpcServer.Serve(lis); err != nil {
			log.Debugf(common.Category, "[ERROR] failed to serve: %v", err)
		}
	}()

loop:
	for c := range r {
		switch c.Cmd {
		case svc.Stop:
			grpcServer.GracefulStop()
			break loop
		default:
			log.Debugf(common.Category, "Unexpected service control request #%d", c)
		}
	}

	changes <- svc.Status{State: svc.StopPending}

	return
}

func runService(port int) error {
	return svc.Run("DobbyVPN vpn service", &managerService{serverPort: port})
}

func run(port int) {
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

	log.Debugf(common.Category, "desktop control listener ready")
	if err := s.Serve(lis); err != nil {
		panic(fmt.Sprintf("failed to serve: %v", err))
	}
}

func (c *Executor) Execute(port int, mode string) {
	if err := initExplicitLocalLog(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialize secure local logging")
		return
	}
	log.Debugf(common.Category, "Executing with mode: %v", mode)

	switch mode {
	case "normal":
		run(port)
	case "service":
		runService(port)
	default:
		log.Debugf(common.Category, "[ERROR] Invalid run mode")
	}
}
