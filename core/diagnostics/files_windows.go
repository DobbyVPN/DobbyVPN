//go:build windows

package diagnostics

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

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
func openAppend(path string) (*os.File, error) {
	return openShared(path, windows.FILE_APPEND_DATA|windows.FILE_READ_ATTRIBUTES, windows.OPEN_ALWAYS)
}
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
