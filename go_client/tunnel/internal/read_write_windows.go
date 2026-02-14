//go:build windows

package internal

import "syscall"

func Read(fd int, buf []byte) (int, error) {
	return syscall.Read(syscall.Handle(fd), buf)
}

func Write(fd int, n int, buf []byte) (int, error) {
	if n <= 0 || n > len(buf) {
		return 0, nil
	}
	return syscall.Write(syscall.Handle(fd), buf[:n])
}
