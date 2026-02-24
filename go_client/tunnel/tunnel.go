package direct

import (
	"net/netip"

	"golang.zx2c4.com/wireguard/tun/netstack"
)

type DirectIPDevice struct {
	dev tun.Device
	net *netstack.Net
}

func NewDirectIPDevice() (*DirectIPDevice, error) {
	local := []netip.Addr{
		netip.MustParseAddr("198.18.0.1"), // виртуальный IP для direct-машрутов
	}

	dns := []netip.Addr{
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("1.1.1.1"),
	}

	// MTU мы возьмем 1500 (или можешь передавать сверху)
	dev, netdev, err := netstack.CreateNetTUN(local, dns, 1500)
	if err != nil {
		return nil, err
	}

	return &DirectIPDevice{
		dev: dev,
		net: netdev,
	}, nil
}

func (d *DirectIPDevice) Write(pkt []byte) (int, error) {
	// netTun.Write принимает [][]byte, offset=0
	buffs := [][]byte{pkt}
	return d.dev.Write(buffs, 0)
}

func (d *DirectIPDevice) Read(buf []byte) (int, error) {
	// Read принимает [][]byte, sizes, offset
	in := [][]byte{buf}
	sizes := []int{0}

	n, err := d.dev.Read(in, sizes, 0)
	if err != nil || n == 0 {
		return 0, err
	}

	return sizes[0], nil
}

func (d *DirectIPDevice) Close() error {
	return d.dev.Close()
}
