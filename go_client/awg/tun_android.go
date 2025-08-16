//go:build android

package awg

import (
	"github.com/amnezia-vpn/amneziawg-go/tun"
	"golang.org/x/sys/unix"
)

func createTUN(fd int) (tun.Device, string, error) {
	return tun.CreateUnmonitoredTUNFromFD(fd)
}
