//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const localLogClearMarker = "{\"schema\":\"dobby.log/v1\",\"event\":\"logs.cleared\",\"source\":\"dobby-cli\"}\n"

var localLogPathMu sync.Mutex

func clearLocalLogFile(path string) error {
	return clearLocalLogFileAtBase(path, filepath.Dir(filepath.Dir(path)))
}

func clearLocalLogFileAtBase(path, base string) error {
	localLogPathMu.Lock()
	defer localLogPathMu.Unlock()

	path, base = filepath.Clean(path), filepath.Clean(base)
	if !filepath.IsAbs(path) || !filepath.IsAbs(base) {
		return fmt.Errorf("local log path and base must be absolute")
	}
	relative, err := filepath.Rel(base, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("local log path is outside its base")
	}
	parent := filepath.Dir(path)
	if err = os.MkdirAll(parent, 0o700); err != nil {
		return fmt.Errorf("create local log directory: %w", err)
	}
	var info os.FileInfo
	if info, err = os.Lstat(parent); err != nil {
		return fmt.Errorf("inspect local log directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("local log directory is not a real directory")
	}
	if info, err = os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("local log path is not a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect local log path: %w", err)
	}
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open local log path: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open local log path: invalid descriptor")
	}
	writeErr := writeLocalLogMarker(file)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func writeLocalLogMarker(file *os.File) error {
	if _, err := file.WriteString(localLogClearMarker); err != nil {
		return fmt.Errorf("write local log clear marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local log clear marker: %w", err)
	}
	return nil
}
