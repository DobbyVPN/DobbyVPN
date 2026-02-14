//go:build android

package internal

import "syscall"

func Read(fd int, buf []byte) (int, error) {
	return syscall.Read(fd, buf)
}

func Write(fd int, n int, buf []byte) (int, error) {
	return syscall.Write(fd, buf[:n])
}
