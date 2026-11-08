//go:build windows

package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// FILE_RENAME_INFO uses native HANDLE alignment and a variable UTF-16 filename.
type fileRenameInfo struct {
	flags          uint32
	rootDirectory  windows.Handle
	fileNameLength uint32
	fileName       [1]uint16
}

func renameGeneration(oldPath, newPath string) (bool, error) {
	file, err := openShared(oldPath, windows.DELETE|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING)
	if err != nil {
		return false, &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}
	}
	absolute, err := filepath.Abs(newPath)
	if err != nil {
		return false, errors.Join(&os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}, file.Close())
	}
	name, err := windows.UTF16FromString(absolute)
	if err != nil {
		return false, errors.Join(&os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: err}, file.Close())
	}
	name = name[:len(name)-1]
	offset := unsafe.Offsetof(fileRenameInfo{}.fileName)
	size := offset + uintptr(len(name))*2
	if size < unsafe.Sizeof(fileRenameInfo{}) {
		size = unsafe.Sizeof(fileRenameInfo{})
	}
	words := make([]uintptr, (size+unsafe.Sizeof(uintptr(0))-1)/unsafe.Sizeof(uintptr(0)))
	info := (*fileRenameInfo)(unsafe.Pointer(&words[0]))
	info.flags = windows.FILE_RENAME_REPLACE_IF_EXISTS | windows.FILE_RENAME_POSIX_SEMANTICS
	info.fileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Add(unsafe.Pointer(info), offset)), len(name)), name)
	callErr := windows.SetFileInformationByHandle(windows.Handle(file.Fd()), windows.FileRenameInfoEx, (*byte)(unsafe.Pointer(info)), uint32(size))
	var renameErr error
	if callErr != nil {
		renameErr = &os.LinkError{Op: "rename", Old: oldPath, New: newPath,
			Err: fmt.Errorf("SetFileInformationByHandle FileRenameInfoEx flags=REPLACE_IF_EXISTS|POSIX_SEMANTICS: %w", callErr)}
	}
	return callErr == nil, errors.Join(renameErr, file.Close())
}

func lockFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &windows.Overlapped{})
}
func unlockFile(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
func openShared(path string, access, disposition uint32) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, errors.Join(fmt.Errorf("diagnostic path is a reparse point: %s", path), file.Close())
	}
	return file, nil
}
func openLock(path string) (*os.File, error) {
	return openShared(path, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.OPEN_ALWAYS)
}
func openLegacyInput(path string) (*os.File, error) { return OpenInput(path) }
func openAppend(path string) (*os.File, error) {
	return openShared(path, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES, windows.OPEN_ALWAYS)
}
func openStartupMarker(path string) (*os.File, error) { return OpenInput(path) }
func openExistingFailedStartup(path string) (*os.File, error) {
	return openShared(path, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES, windows.OPEN_EXISTING)
}
func openFailedStartup(path string) (*os.File, error) { return openAppend(path) }
func OpenInput(path string) (*os.File, error) {
	return openShared(path, windows.GENERIC_READ, windows.OPEN_EXISTING)
}

func readLockFile(file *os.File) error {
	return windows.LockFileEx(windows.Handle(file.Fd()), 0, 0, 1, 0, &windows.Overlapped{})
}
func preservePermissions(_ *os.File, _ os.FileInfo) error { return nil }

func Identity(file *os.File) (string, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d:%d", info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow), nil
}
