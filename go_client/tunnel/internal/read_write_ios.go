//go:build ios || darwin

package internal

import (
	"encoding/binary"
	"golang.org/x/sys/unix"
	"syscall"
)

func Read(fd int, buf []byte) (int, error) {
	n, err := syscall.Read(fd, buf)
	if err != nil {
		return 0, err
	}
	if n <= 4 {
		return 0, nil
	}

	copy(buf, buf[4:n])
	return n - 4, nil
}

func Write(fd int, n int, buf []byte) {
	if n <= 0 || n > len(buf) {
		return
	}

	ver := buf[0] >> 4

	var af uint32
	switch ver {
	case 4:
		af = uint32(unix.AF_INET)
	case 6:
		af = uint32(unix.AF_INET6)
	default:
		return
	}

	out := make([]byte, 4+n)
	binary.BigEndian.PutUint32(out[:4], af)
	copy(out[4:], buf[:n])

	_, _ = syscall.Write(fd, out)
}
