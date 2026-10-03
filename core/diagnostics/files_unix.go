//go:build !windows

package diagnostics

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func lockFile(file *os.File) error   { return unix.Flock(int(file.Fd()), unix.LOCK_EX) }
func unlockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }
func openLocalFile(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
func openAppend(path string) (*os.File, error) {
	return openLocalFile(path, unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND)
}
func openLock(path string) (*os.File, error)  { return openLocalFile(path, unix.O_CREAT|unix.O_RDWR) }
func OpenInput(path string) (*os.File, error) { return os.Open(path) }

func readLockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_SH) }
func preservePermissions(file *os.File, info os.FileInfo) error {
	if err := file.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
		return file.Chown(int(stat.Uid), int(stat.Gid))
	}
	return nil
}

func Identity(file *os.File) (string, error) {
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", os.ErrInvalid
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}
