//go:build !(windows || android || ios)

package executor

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"go_module/desktop_exports/controlplane"
	"go_module/desktop_exports/proto"
	"go_module/grpcproto"

	"go_module/log"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

func explicitLogRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Abs(root)
}

func openManagedLocalLog(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_APPEND|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("managed log descriptor is unavailable")
	}
	return file, nil
}

func initExplicitLocalLog() error {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	if requested == "" {
		return nil
	}
	root, err := explicitLogRoot()
	if err != nil {
		return err
	}
	path, err := filepath.Abs(requested)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("explicit log path is outside the local temporary directory")
	}
	parent := filepath.Dir(path)
	managed := strings.TrimSpace(os.Getenv("DOBBY_LOG_PRECREATED")) == "1"
	if managed {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return fmt.Errorf("managed explicit log target is unavailable: %w", statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("managed explicit log target must be a regular file")
		}
		resolvedRoot, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return resolveErr
		}
		resolvedParent, resolveErr := filepath.EvalSymlinks(parent)
		if resolveErr != nil {
			return resolveErr
		}
		relative, resolveErr = filepath.Rel(resolvedRoot, resolvedParent)
		if resolveErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fmt.Errorf("managed explicit log path traverses outside its root")
		}
		file, openErr := openManagedLocalLog(path)
		if openErr != nil {
			return openErr
		}
		return log.SetOpenedFile(file)
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	relative, err = filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("explicit log path traverses outside the local temporary directory")
	}
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("explicit log target must be a regular file")
		}
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := log.SetPath(path); err != nil {
		return err
	}
	return nil
}

func run(_ int) {
	if err := initExplicitLocalLog(); err != nil {
		panic(fmt.Sprintf("failed to initialize secure local logging: %v", err))
	}
	// Convert logrus.Fatal (os.Exit) into a panic so goroutines can recover from it
	// instead of crashing the entire gRPC server process.
	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(fmt.Sprintf("fatal error (exit code %d)", code))
	}
	if err := recoverInterruptedState(); err != nil {
		panic(fmt.Sprintf("failed to recover interrupted product state: %v", err))
	}

	flag.Parse()
	lis, err := controlplane.ListenControlSocket()
	if err != nil {
		panic(fmt.Sprintf("failed to listen: %v", err))
	}
	s := grpc.NewServer(
		grpc.Creds(controlplane.UnixPeerCredentials{}),
		grpc.ChainUnaryInterceptor(
			proto.ControlAuthUnaryInterceptor(false, ""),
			proto.PanicRecoveryUnaryInterceptor(),
			proto.ErrorLoggingUnaryInterceptor(),
		),
	)

	grpcproto.RegisterVpnServer(s, &proto.Server{})

	log.Debugf(desktopLogCategory, "desktop control socket ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := controlplane.ServeUntilSignal(s, lis, signals); err != nil {
		panic(fmt.Sprintf("failed to serve: %v", err))
	}
}

func (c *Executor) Execute(port int, mode string) {
	switch mode {
	case "normal":
		run(port)
	default:
		log.Debugf(desktopLogCategory, "[ERROR] Invalid run mode")
	}
}
