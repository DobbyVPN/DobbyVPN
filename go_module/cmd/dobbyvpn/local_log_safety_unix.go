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

// Active log operations are serialized in-process. Directory descriptors are
// opened without following symlinks, so clearing a log cannot be redirected
// through a replaced ancestor or final entry.
var localLogPathMu sync.Mutex

// clearLocalLogFile clears only the current regular owner-local log file and
// leaves a machine-readable boundary marker. Existing entries are pinned by a
// no-follow descriptor before the write descriptor is opened; the two file
// identities must match before truncation.
func clearLocalLogFile(path string) error {
	return clearLocalLogFileAtBase(path, filepath.Dir(filepath.Dir(path)))
}

func clearLocalLogFileAtBase(path, base string) error {
	localLogPathMu.Lock()
	defer localLogPathMu.Unlock()

	if !filepath.IsAbs(path) {
		return fmt.Errorf("local log path must be absolute")
	}
	if !filepath.IsAbs(base) {
		return fmt.Errorf("local log base must be absolute")
	}
	absBase := filepath.Clean(base)
	cleanPath := filepath.Clean(path)
	relativePath, err := filepath.Rel(absBase, cleanPath)
	if err != nil || relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || filepath.IsAbs(relativePath) {
		return fmt.Errorf("local log path is outside pinned base")
	}
	parentPath := filepath.Dir(cleanPath)
	relativeParent := filepath.Dir(relativePath)
	if parentPath == absBase {
		relativeParent = "."
	}

	// The base is an existing trust boundary. It is never created through the
	// absolute path, and every descendant is opened or created relative to the
	// already-pinned descriptor. This prevents a replaced ancestor from
	// redirecting either directory creation or the active-file mutation.
	baseFile, baseInfo, err := openPinnedLocalLogBase(absBase)
	if err != nil {
		return fmt.Errorf("open local log base: %w", err)
	}
	defer baseFile.Close()
	directories := []*os.File{baseFile}
	defer func() {
		for _, directory := range directories[1:] {
			_ = directory.Close()
		}
	}()
	parent := baseFile
	if relativeParent != "." {
		for _, component := range splitLocalLogPath(relativeParent) {
			directory, openErr := openPinnedLocalLogChild(parent, component, true)
			if openErr != nil {
				return fmt.Errorf("open local log directory component %q: %w", component, openErr)
			}
			directories = append(directories, directory)
			parent = directory
		}
	}
	if _, err := verifyPinnedLocalLogDirectory(baseFile, baseInfo, absBase); err != nil {
		return fmt.Errorf("verify local log base before file mutation: %w", err)
	}

	name := filepath.Base(cleanPath)
	exists, regular, err := localLogEntry(parent, name)
	if err != nil {
		return fmt.Errorf("inspect local log path: %w", err)
	}
	if exists && !regular {
		return fmt.Errorf("local log path is not a regular file")
	}

	var expectedInfo os.FileInfo
	if exists {
		expectedInfo, err = openLocalLogEntryInfo(parent, name)
		if err != nil {
			return fmt.Errorf("pin local log path: %w", err)
		}
	}
	flags := unix.O_WRONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if !exists {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0o600)
	if err != nil {
		return fmt.Errorf("open local log path: %w", err)
	}
	file := os.NewFile(uintptr(fd), cleanPath)
	if file == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("open local log path: invalid file descriptor")
	}
	closeFile := func() error { return file.Close() }
	defer func() { _ = closeFile() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("verify local log path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("local log path is not a regular file")
	}
	if expectedInfo != nil && !os.SameFile(expectedInfo, info) {
		return fmt.Errorf("local log path changed between inspection and open")
	}
	verifyActiveEntry := func(stage string) error {
		currentInfo, verifyErr := openLocalLogEntryInfo(parent, name)
		if verifyErr != nil {
			return fmt.Errorf("%s local log path: %w", stage, verifyErr)
		}
		if !os.SameFile(info, currentInfo) {
			return fmt.Errorf("%s local log path changed", stage)
		}
		return nil
	}
	if err := verifyActiveEntry("recheck before truncate"); err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("secure local log path: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate local log path: %w", err)
	}
	if _, err := file.WriteString(localLogClearMarker); err != nil {
		return fmt.Errorf("write local log clear marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local log clear marker: %w", err)
	}
	if err := verifyActiveEntry("recheck after sync"); err != nil {
		return err
	}
	if _, err := verifyPinnedLocalLogDirectory(baseFile, baseInfo, absBase); err != nil {
		return fmt.Errorf("verify local log base after file mutation: %w", err)
	}
	if err := closeFile(); err != nil {
		return fmt.Errorf("close local log path: %w", err)
	}
	return nil
}

func splitLocalLogPath(relative string) []string {
	if relative == "." || relative == "" {
		return nil
	}
	components := strings.Split(relative, string(filepath.Separator))
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			// filepath.Rel should already have normalized these, but keeping the
			// check next to the descriptor traversal makes the invariant explicit.
			return []string{".."}
		}
	}
	return components
}

func openPinnedLocalLogBase(path string) (*os.File, unix.Stat_t, error) {
	if !filepath.IsAbs(path) {
		return nil, unix.Stat_t{}, fmt.Errorf("directory path must be absolute")
	}
	path = filepath.Clean(path)
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	current := os.NewFile(uintptr(fd), string(filepath.Separator))
	if current == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, fmt.Errorf("open local log base: invalid file descriptor")
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		info, verifyErr := verifyPinnedLocalLogDirectory(current, unix.Stat_t{}, path)
		if verifyErr != nil {
			_ = current.Close()
			return nil, unix.Stat_t{}, verifyErr
		}
		return current, info, nil
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			_ = current.Close()
			return nil, unix.Stat_t{}, fmt.Errorf("invalid local log base component %q", component)
		}
		nextFD, openErr := unix.Openat(
			int(current.Fd()),
			component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		if openErr != nil {
			_ = current.Close()
			return nil, unix.Stat_t{}, openErr
		}
		next := os.NewFile(uintptr(nextFD), filepath.Join(current.Name(), component))
		if next == nil {
			_ = unix.Close(nextFD)
			_ = current.Close()
			return nil, unix.Stat_t{}, fmt.Errorf("open local log base: invalid file descriptor")
		}
		_ = current.Close()
		current = next
	}
	info, verifyErr := verifyPinnedLocalLogDirectory(current, unix.Stat_t{}, path)
	if verifyErr != nil {
		_ = current.Close()
		return nil, unix.Stat_t{}, verifyErr
	}
	return current, info, nil
}

func openPinnedLocalLogChild(parent *os.File, component string, create bool) (*os.File, error) {
	if component == "" || component == "." || component == ".." {
		return nil, fmt.Errorf("invalid directory component")
	}
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(int(parent.Fd()), component, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if mkdirErr := unix.Mkdirat(int(parent.Fd()), component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
			return nil, mkdirErr
		}
		fd, err = unix.Openat(int(parent.Fd()), component, flags, 0)
	}
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), component)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open local log directory: invalid file descriptor")
	}
	if _, verifyErr := verifyPinnedLocalLogDirectory(file, unix.Stat_t{}, component); verifyErr != nil {
		_ = file.Close()
		return nil, verifyErr
	}
	return file, nil
}

func verifyPinnedLocalLogDirectory(file *os.File, expected unix.Stat_t, path string) (unix.Stat_t, error) {
	var info unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &info); err != nil {
		return unix.Stat_t{}, err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Stat_t{}, fmt.Errorf("%q is not a directory", path)
	}
	if uint32(info.Uid) != uint32(os.Geteuid()) {
		return unix.Stat_t{}, fmt.Errorf("%q is not owned by the effective user", path)
	}
	if uint32(info.Mode)&0o022 != 0 {
		return unix.Stat_t{}, fmt.Errorf("%q is group/other writable", path)
	}
	if expected.Mode != 0 && (info.Dev != expected.Dev || info.Ino != expected.Ino) {
		return unix.Stat_t{}, fmt.Errorf("%q changed while pinned", path)
	}
	return info, nil
}

func openLocalLogDirectory(path string, create bool) (*os.File, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("directory path must be absolute")
	}
	path = filepath.Clean(path)
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	current := os.NewFile(uintptr(fd), string(filepath.Separator))
	if current == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open directory: invalid file descriptor")
	}
	if _, verifyErr := verifyPinnedLocalLogDirectory(current, unix.Stat_t{}, string(filepath.Separator)); verifyErr != nil {
		_ = current.Close()
		return nil, verifyErr
	}

	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 1 && components[0] == "" {
		return current, nil
	}
	for _, component := range components {
		if component == "" || component == "." {
			continue
		}
		nextFD, openErr := unix.Openat(
			int(current.Fd()), component,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
			0,
		)
		if errors.Is(openErr, unix.ENOENT) && create {
			if mkdirErr := unix.Mkdirat(int(current.Fd()), component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				_ = current.Close()
				return nil, mkdirErr
			}
			nextFD, openErr = unix.Openat(
				int(current.Fd()), component,
				unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW,
				0,
			)
		}
		if openErr != nil {
			_ = current.Close()
			return nil, openErr
		}
		next := os.NewFile(uintptr(nextFD), filepath.Join(filepath.Dir(current.Name()), component))
		if next == nil {
			_ = unix.Close(nextFD)
			_ = current.Close()
			return nil, fmt.Errorf("open directory: invalid file descriptor")
		}
		_ = current.Close()
		current = next
		if _, verifyErr := verifyPinnedLocalLogDirectory(current, unix.Stat_t{}, current.Name()); verifyErr != nil {
			_ = current.Close()
			return nil, verifyErr
		}
	}
	return current, nil
}

func localLogEntry(parent *os.File, name string) (exists, regular bool, err error) {
	var info unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return false, false, nil
		}
		return false, false, err
	}
	mode := info.Mode & unix.S_IFMT
	if mode == unix.S_IFLNK {
		return true, false, fmt.Errorf("local log path is an alias")
	}
	return true, mode == unix.S_IFREG, nil
}

func openLocalLogFile(parent *os.File, name string, flags int, mode uint32) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, flags, mode)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open local log path: invalid file descriptor")
	}
	return file, nil
}

func regularLogFileInfo(file *os.File) (os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("local log path is not a regular file")
	}
	return info, nil
}

func openLocalLogEntryInfo(parent *os.File, name string) (os.FileInfo, error) {
	file, err := openLocalLogFile(
		parent,
		name,
		unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK,
		0,
	)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return regularLogFileInfo(file)
}
