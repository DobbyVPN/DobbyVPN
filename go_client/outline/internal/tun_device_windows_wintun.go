//go:build windows

package internal

import (
	"fmt"
	"net/netip"
	"time"

	"golang.zx2c4.com/wintun"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"

	"github.com/Jigsaw-Code/outline-sdk/network"
	log "go_client/logger"
)

type wintunDevice struct {
	session wintun.Session
	iface   *wintun.Adapter
	name    string
}

var _ network.IPDevice = (*wintunDevice)(nil)

func NewTunDevice(name, ip string) (network.IPDevice, error) {
	const mtu = 1500

	log.Infof("[Wintun] Creating adapter...")

	adapter, err := wintun.CreateAdapter(name, "DobbyVPN", nil)
	if err != nil {
		return nil, fmt.Errorf("CreateAdapter failed: %w", err)
	}

	session, err := adapter.StartSession(0x400000) // 4MB buffer
	if err != nil {
		adapter.Close()
		return nil, fmt.Errorf("StartSession failed: %w", err)
	}

	dev := &wintunDevice{
		session: session,
		iface:   adapter,
		name:    name,
	}

	log.Infof("[Wintun] Adapter created: %s", name)

	if err := dev.configureIP(ip); err != nil {
		dev.Close()
		return nil, err
	}

	time.Sleep(500 * time.Millisecond)

	return dev, nil
}

func (d *wintunDevice) Read(p []byte) (int, error) {
	packet, err := d.session.ReceivePacket()
	if err != nil {
		return 0, err
	}
	copy(p, packet)
	d.session.ReleaseReceivePacket(packet)
	return len(packet), nil
}

func (d *wintunDevice) Write(p []byte) (int, error) {
	packet, err := d.session.AllocateSendPacket(len(p))
	if err != nil {
		return 0, err
	}
	copy(packet, p)
	d.session.SendPacket(packet)
	return len(p), nil
}

func (d *wintunDevice) Close() error {
	d.session.End()
	d.iface.Close()
	return nil
}

func (d *wintunDevice) MTU() int {
	return 1500
}

func (d *wintunDevice) configureIP(ip string) error {
	log.Infof("[Wintun] Configuring IP: %s", ip)

	luid := winipcfg.LUID(d.iface.LUID())

	addr, err := netip.ParsePrefix(ip + "/24")
	if err != nil {
		return fmt.Errorf("failed to parse prefix: %w", err)
	}

	err = luid.SetIPAddresses([]netip.Prefix{addr})
	if err != nil {
		return fmt.Errorf("SetIPAddresses failed: %w", err)
	}

	return nil
}
