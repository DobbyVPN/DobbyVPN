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
)

func explicitLogRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
	if root == "" {
		root = os.TempDir()
	}
	return filepath.Abs(root)
}

func prepareLocalLogPath() (string, error) {
	root, path, err := explicitLocalLogPaths()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(os.Getenv("DOBBY_LOG_PRECREATED")) == "1" {
		err = validateManagedLocalLog(root, path)
	} else {
		err = prepareUnmanagedLocalLog(root, path)
	}
	return path, err
}

func explicitLocalLogPaths() (logRoot, logPath string, resultErr error) {
	requested := strings.TrimSpace(os.Getenv("DOBBY_LOG_PATH"))
	root := strings.TrimSpace(os.Getenv("DOBBY_LOG_ROOT"))
	if requested == "" {
		root, requested = defaultDesktopLogPath()
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

func prepareUnmanagedLocalLog(root, path string) error {
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
	return nil
}

func defaultDesktopLogPath() (root, path string) {
	root = "/var/log/dobbyvpn"
	if runtime.GOOS == darwinPlatform {
		root = "/Library/Logs/DobbyVPN"
	}
	return root, filepath.Join(root, "backend.jsonl")
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
	if err := shutdownDesktop(listener.Close, serveErr, desktopProcessBinding()); err != nil {
		// Report through the log and exit non-zero instead of panicking: a
		// stack trace would bury the cleanup error and add no information.
		log.Errorf(desktopLogCategory, "desktop shutdown failed: %v", err)
		fmt.Fprintf(os.Stderr, "desktop shutdown failed: %v\n", err)
		os.Exit(1)
	}
}

func (c *Executor) Execute(mode string) {
	switch mode {
	case "normal":
		run()
	default:
		log.Errorf(desktopLogCategory, "Invalid run mode")
	}
}
