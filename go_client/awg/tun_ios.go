//go:build ios

package awg

import (
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
	"os"
)

func createTUN(fd int) (tun.Device, string, error) {
	dupTunFd, err := unix.Dup(fd)
	if err != nil {
		return nil, "", err
	}

	err = unix.SetNonblock(dupTunFd, true)
	if err != nil {
		unix.Close(dupTunFd)
		return nil, "", err
	}

	dev, err := tun.CreateTUNFromFile(os.NewFile(uintptr(dupTunFd), "/dev/tun"), 0)
	return dev, "tun", err
}
