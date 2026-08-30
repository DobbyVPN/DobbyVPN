//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"go_module/desktop_exports/controlplane"
	"golang.org/x/sys/windows"
)

const localLogClearMarker = "{\"schema\":\"dobby.log/v1\",\"event\":\"logs.cleared\",\"source\":\"dobby-cli\"}\n"

var localLogPathMu sync.Mutex

// The active Windows log is opened with no reparse-point following and no
// delete sharing. The final entry identity and handle identity are compared
// before truncation; the handle's final pathname is checked again afterward.
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
	path = filepath.Clean(path)
	base = filepath.Clean(base)
	if err := validateWindowsAncestors(base); err != nil {
		return fmt.Errorf("validate local log base: %w", err)
	}
	// Validate the pre-existing trust boundary before the descriptor-relative
	// traversal is allowed to create .dobbyvpn or any other descendant. The
	// pinned traversal repeats this check after opening the no-reparse base
	// handle to close the path/ACL race between these observations.
	if err := controlplane.VerifyUserConfigBasePermissions(base); err != nil {
		return fmt.Errorf("validate local log base ACL: %w", err)
	}
	directoryPins, err := openWindowsDirectoryPins(base, filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("pin local log directory: %w", err)
	}
	defer closeWindowsDirectoryPins(directoryPins)
	if err := secureWindowsOwnerOnlyPath(filepath.Dir(path)); err != nil {
		return fmt.Errorf("secure local log directory ACL: %w", err)
	}
	if err := verifyWindowsDirectoryPins(directoryPins); err != nil {
		return fmt.Errorf("verify local log directory after ACL: %w", err)
	}
	exists, regular, err := windowsLocalLogEntry(path)
	if err != nil {
		return fmt.Errorf("inspect local log path: %w", err)
	}
	if exists && !regular {
		return fmt.Errorf("local log path is not a regular file")
	}

	var expected windowsFileIdentity
	if exists {
		expected, err = windowsPathIdentity(path)
		if err != nil {
			return fmt.Errorf("pin local log path: %w", err)
		}
	}
	creation := uint32(windows.OPEN_EXISTING)
	if !exists {
		creation = windows.CREATE_NEW
	}
	file, err := openWindowsRegularLogFile(
		path,
		windows.GENERIC_WRITE|windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		creation,
	)
	if err != nil {
		return fmt.Errorf("open local log path: %w", err)
	}
	handle := windows.Handle(file.Fd())
	closeFile := func() error { return file.Close() }
	defer func() { _ = closeFile() }()
	openedIdentity, err := windowsHandleIdentity(handle)
	if err != nil {
		return fmt.Errorf("identify opened local log path: %w", err)
	}
	if exists && !sameWindowsFileIdentity(expected, openedIdentity) {
		return fmt.Errorf("local log path changed between inspection and open")
	}
	if err := verifyWindowsDirectoryPins(directoryPins); err != nil {
		return fmt.Errorf("verify local log directory before truncate: %w", err)
	}
	if err := secureWindowsOwnerOnlyPath(path); err != nil {
		return fmt.Errorf("secure local log ACL: %w", err)
	}
	securedIdentity, err := windowsHandleIdentity(handle)
	if err != nil {
		return fmt.Errorf("identify secured local log path: %w", err)
	}
	if !sameWindowsFileIdentity(openedIdentity, securedIdentity) {
		return fmt.Errorf("local log path changed while securing ACL")
	}
	if err := verifyWindowsHandlePath(handle, path); err != nil {
		return fmt.Errorf("verify local log path before truncate: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate local log path: %w", err)
	}
	if err := verifyWindowsHandlePath(handle, path); err != nil {
		return fmt.Errorf("verify local log path after truncate: %w", err)
	}
	activeIdentity, err := windowsPathIdentity(path)
	if err != nil {
		return fmt.Errorf("verify active local log path: %w", err)
	}
	if !sameWindowsFileIdentity(openedIdentity, activeIdentity) {
		return fmt.Errorf("local log path changed after truncate")
	}
	if err := verifyWindowsDirectoryPins(directoryPins); err != nil {
		return fmt.Errorf("verify local log directory after truncate: %w", err)
	}
	if _, err := file.WriteString(localLogClearMarker); err != nil {
		return fmt.Errorf("write local log clear marker: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync local log clear marker: %w", err)
	}
	if err := verifyWindowsHandlePath(handle, path); err != nil {
		return fmt.Errorf("verify local log path after sync: %w", err)
	}
	finalIdentity, err := windowsPathIdentity(path)
	if err != nil {
		return fmt.Errorf("verify active local log path after sync: %w", err)
	}
	if !sameWindowsFileIdentity(openedIdentity, finalIdentity) {
		return fmt.Errorf("local log path changed after sync")
	}
	if err := verifyWindowsDirectoryPins(directoryPins); err != nil {
		return fmt.Errorf("verify local log directory after sync: %w", err)
	}
	return closeFile()
}

func validateWindowsAncestors(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("directory path must be absolute")
	}
	volume := filepath.VolumeName(path)
	root := volume + string(os.PathSeparator)
	rest := strings.TrimPrefix(path, root)
	if rest == "" {
		return nil
	}
	current := root
	for _, component := range strings.FieldsFunc(rest, func(r rune) bool { return r == '\\' || r == '/' }) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("directory component %q is an alias", current)
		}
		if !info.IsDir() {
			return fmt.Errorf("directory component %q is not a directory", current)
		}
	}
	return nil
}

func windowsLocalLogEntry(path string) (exists, regular bool, err error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, false, fmt.Errorf("local log path is an alias")
	}
	return true, info.Mode().IsRegular(), nil
}

type windowsFileIdentity struct {
	volume uint32
	index  uint64
}

func windowsHandleIdentity(handle windows.Handle) (windowsFileIdentity, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return windowsFileIdentity{}, err
	}
	return windowsFileIdentity{
		volume: info.VolumeSerialNumber,
		index:  uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	}, nil
}

func sameWindowsFileIdentity(left, right windowsFileIdentity) bool {
	return left.volume == right.volume && left.index == right.index
}

func windowsPathIdentity(path string) (windowsFileIdentity, error) {
	file, err := openWindowsRegularLogFile(
		path,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.OPEN_EXISTING,
	)
	if err != nil {
		return windowsFileIdentity{}, err
	}
	defer file.Close()
	return windowsHandleIdentity(windows.Handle(file.Fd()))
}

type windowsDirectoryPin struct {
	path string
	file *os.File
	id   windowsFileIdentity
}

func openWindowsDirectoryPins(base, path string) ([]windowsDirectoryPin, error) {
	if !filepath.IsAbs(base) || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("local log directories must be absolute")
	}
	base = filepath.Clean(base)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(base, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return nil, fmt.Errorf("local log directory is outside pinned base")
	}
	baseFile, baseIdentity, err := openWindowsDirectory(base)
	if err != nil {
		return nil, err
	}
	if err := verifyWindowsHandlePath(windows.Handle(baseFile.Fd()), base); err != nil {
		_ = baseFile.Close()
		return nil, fmt.Errorf("verify pinned base directory: %w", err)
	}
	pins := []windowsDirectoryPin{{path: base, file: baseFile, id: baseIdentity}}
	// Validate the existing trust boundary while its no-reparse base handle is
	// pinned, before any descendant can be created. Untrusted identities may
	// retain read-only ACEs, but no write/delete/ACL/owner access is accepted.
	if err := controlplane.VerifyUserConfigBasePermissions(base); err != nil {
		_ = baseFile.Close()
		return nil, fmt.Errorf("verify pinned base ACL: %w", err)
	}
	if err := verifyWindowsDirectoryPins(pins); err != nil {
		closeWindowsDirectoryPins(pins)
		return nil, fmt.Errorf("verify pinned base before descendant creation: %w", err)
	}
	current := base
	if relative == "." {
		return pins, nil
	}
	for _, component := range strings.FieldsFunc(relative, func(r rune) bool { return r == '\\' || r == '/' }) {
		current = filepath.Join(current, component)
		if err := verifyWindowsDirectoryPins(pins); err != nil {
			closeWindowsDirectoryPins(pins)
			return nil, fmt.Errorf("verify pinned base before creating %q: %w", current, err)
		}
		// The base handle is already pinned before any missing descendant is
		// created. This prevents a replacement of the user's config root from
		// redirecting creation of the active directory.
		if _, statErr := os.Lstat(current); errors.Is(statErr, os.ErrNotExist) {
			// Windows ignores the Unix mode bits for access control; the explicit
			// DACL is installed and verified immediately after pinning this entry.
			if mkdirErr := os.Mkdir(current, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				closeWindowsDirectoryPins(pins)
				return nil, mkdirErr
			}
		} else if statErr != nil {
			closeWindowsDirectoryPins(pins)
			return nil, statErr
		}
		file, identity, openErr := openWindowsDirectory(current)
		if openErr != nil {
			closeWindowsDirectoryPins(pins)
			return nil, openErr
		}
		pins = append(pins, windowsDirectoryPin{path: current, file: file, id: identity})
		if err := verifyWindowsDirectoryPins(pins); err != nil {
			closeWindowsDirectoryPins(pins)
			return nil, fmt.Errorf("verify pinned directories after creating %q: %w", current, err)
		}
	}
	return pins, nil
}

func secureWindowsOwnerOnlyPath(path string) error {
	if err := controlplane.SecureExplicitUserPath(path); err != nil {
		return err
	}
	if err := controlplane.VerifyExplicitUserPathPermissions(path); err != nil {
		return err
	}
	return nil
}

func openWindowsDirectory(path string) (*os.File, windowsFileIdentity, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, windowsFileIdentity{}, err
	}
	handle, err := windows.CreateFile(
		name,
		windows.GENERIC_READ|windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, windowsFileIdentity{}, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, windowsFileIdentity{}, fmt.Errorf("open local log directory: invalid file handle")
	}
	exists, regular, err := windowsHandleLogEntry(handle)
	if err != nil {
		_ = file.Close()
		return nil, windowsFileIdentity{}, err
	}
	if !exists || regular {
		_ = file.Close()
		return nil, windowsFileIdentity{}, fmt.Errorf("local log directory is not a directory")
	}
	identity, err := windowsHandleIdentity(handle)
	if err != nil {
		_ = file.Close()
		return nil, windowsFileIdentity{}, err
	}
	return file, identity, nil
}

func closeWindowsDirectoryPins(pins []windowsDirectoryPin) {
	for _, pin := range pins {
		_ = pin.file.Close()
	}
}

func verifyWindowsDirectoryPins(pins []windowsDirectoryPin) error {
	for _, pin := range pins {
		identity, err := windowsHandleIdentity(windows.Handle(pin.file.Fd()))
		if err != nil {
			return fmt.Errorf("identify %q: %w", pin.path, err)
		}
		if !sameWindowsFileIdentity(pin.id, identity) {
			return fmt.Errorf("directory %q changed", pin.path)
		}
		if err := verifyWindowsHandlePath(windows.Handle(pin.file.Fd()), pin.path); err != nil {
			return fmt.Errorf("directory %q path changed: %w", pin.path, err)
		}
	}
	return nil
}

func openWindowsRegularLogFile(path string, access, share, creation uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		name,
		access,
		share,
		nil,
		creation,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open local log path: invalid file handle")
	}
	if exists, regular, err := windowsHandleLogEntry(handle); err != nil {
		_ = file.Close()
		return nil, err
	} else if !exists || !regular {
		_ = file.Close()
		return nil, fmt.Errorf("local log path is not a regular file")
	}
	if err := verifyWindowsHandlePath(handle, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func windowsHandleLogEntry(handle windows.Handle) (exists, regular bool, err error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return false, false, err
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return true, false, fmt.Errorf("local log path is a reparse point")
	}
	return true, info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0, nil
}

func verifyWindowsHandlePath(handle windows.Handle, expected string) error {
	actual, err := windowsFinalPath(handle)
	if err != nil {
		return err
	}
	expectedAbsolute, err := filepath.Abs(expected)
	if err != nil {
		return err
	}
	if !strings.EqualFold(normalizeWindowsFinalPath(actual), normalizeWindowsFinalPath(expectedAbsolute)) {
		return fmt.Errorf("opened path does not match requested path")
	}
	return nil
}

func windowsFinalPath(handle windows.Handle) (string, error) {
	buffer := make([]uint16, 32*1024)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
	if err != nil {
		return "", err
	}
	if length >= uint32(len(buffer)) {
		return "", fmt.Errorf("opened path is too long")
	}
	return windows.UTF16ToString(buffer[:length]), nil
}

func normalizeWindowsFinalPath(path string) string {
	path = filepath.Clean(path)
	if strings.HasPrefix(path, `\\?\UNC\`) {
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	}
	return strings.TrimPrefix(path, `\\?\`)
}
