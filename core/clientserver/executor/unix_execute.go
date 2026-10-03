//go:build !(windows || android || ios)

package executor

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"core/clientserver/controljson"
	"core/clientserver/controlplane"

	"core/log"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
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
	root, path, err := explicitLocalLogPaths()
	if err != nil {
		return err
	}
	if strings.TrimSpace(os.Getenv("DOBBY_LOG_PRECREATED")) == "1" {
		return initManagedLocalLog(root, path)
	}
	return initUnmanagedLocalLog(root, path)
}

func explicitLocalLogPaths() (logRoot, logPath string, resultErr error) {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
	if requested == "" {
		var pathErr error
		root, requested, pathErr = defaultDesktopLogPath()
		if pathErr != nil {
			return "", "", pathErr
		}
	} else if root == "" {
		var rootErr error
		root, rootErr = explicitLogRoot()
		if rootErr != nil {
			return "", "", rootErr
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	path, err := filepath.Abs(requested)
	if err != nil {
		return "", "", err
	}
	if err := requireLocalLogPath(root, path, "explicit log path is outside the local temporary directory"); err != nil {
		return "", "", err
	}
	return root, path, nil
}

func requireLocalLogPath(root, path, message string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New(message)
	}
	return nil
}

func initManagedLocalLog(root, path string) error {
	if err := validateManagedLocalLog(root, path); err != nil {
		return err
	}
	file, err := openManagedLocalLog(path)
	if err != nil {
		return err
	}
	return log.SetOpenedFile(file)
}

func validateManagedLocalLog(root, path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("managed explicit log target is unavailable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("managed explicit log target must be a regular file")
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	return requireLocalLogPath(resolvedRoot, resolvedParent, "managed explicit log path traverses outside its root")
}

func initUnmanagedLocalLog(root, path string) error {
	parent := filepath.Dir(path)
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
	if err := requireLocalLogPath(resolvedRoot, resolvedParent, "explicit log path traverses outside the local temporary directory"); err != nil {
		return err
	}
	info, statErr := os.Lstat(path)
	switch {
	case statErr == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("explicit log target must be a regular file")
		}
	case !os.IsNotExist(statErr):
		return statErr
	}
	return log.SetPath(path)
}

func defaultDesktopLogPath() (root, path string, err error) {
	switch {
	case runtime.GOOS == "darwin" && os.Getuid() == 0:
		root = "/Library/Logs/DobbyVPN"
	case os.Getuid() == 0:
		root = "/var/log/dobbyvpn"
	default:
		var base string
		base, err = os.UserConfigDir()
		if err != nil {
			return "", "", err
		}
		root = filepath.Join(base, "DobbyVPN", "Logs")
	}
	return root, filepath.Join(root, "backend.jsonl"), nil
}

func run() {
	if err := initExplicitLocalLog(); err != nil {
		panic(fmt.Sprintf("failed to initialize local logging: %v", err))
	}
	// Convert logrus.Fatal (os.Exit) into a panic so goroutines can recover from it
	// instead of crashing the entire desktop control process.
	logrus.StandardLogger().ExitFunc = func(code int) {
		panic(fmt.Sprintf("fatal error (exit code %d)", code))
	}
	if err := recoverInterruptedState(); err != nil {
		panic(fmt.Sprintf("failed to recover interrupted product state: %v", err))
	}
	listener, err := controlplane.ListenControlSocket()
	if err != nil {
		panic(fmt.Sprintf("failed to listen for desktop control: %v", err))
	}
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- controljson.Serve(listener, controljson.Handler{Binding: desktopProcessBinding()}, controlplane.AuthenticateLocalPeer)
	}()
	log.Debugf(desktopLogCategory, "desktop JSON control socket ready")
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	var serveErr error
	select {
	case <-signals:
	case serveErr = <-serveDone:
	}
	if err := shutdownDesktop(listener.Close, serveErr); err != nil {
		panic(fmt.Sprintf("desktop shutdown failed: %v", err))
	}
}

func (c *Executor) Execute(mode string) {
	switch mode {
	case "normal":
		run()
	default:
		log.Debugf(desktopLogCategory, "[ERROR] Invalid run mode")
	}
}
