package direct

import (
	"context"
	"net"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
)

// DirectIPDevice – адаптер вокруг lwIP-девайса,
// чтобы снаружи он выглядел как твой старый DirectIPDevice.
type DirectIPDevice struct {
	dev network.IPDevice
}

// directUDPListener – минимальный transport.PacketListener,
// который просто создаёт локальный UDP-сокет,
// через который можно писать в любой destination (host:port).
type directUDPListener struct{}

// ListenPacket создаёт net.PacketConn, который будет использоваться
// PacketListenerProxy’ем для отправки/приёма UDP-пакетов.
func (l directUDPListener) ListenPacket(ctx context.Context) (net.PacketConn, error) {
	var lc net.ListenConfig
	// 0.0.0.0:0 – «любой адрес / любой свободный порт»
	return lc.ListenPacket(ctx, "udp", "0.0.0.0:0")
}

// NewDirectIPDevice поднимает lwIP-девайс, который ходит напрямую в интернет
// через system TCP/UDP (без Shadowsocks/Outline/Cloak).
func NewDirectIPDevice() (*DirectIPDevice, error) {
	// TCP: обычный TCP-диалер. Внутри он использует net.Dialer с системным DNS.
	// Документация: transport.TCPDialer реализует StreamDialer. :contentReference[oaicite:1]{index=1}
	tcpDialer := &transport.TCPDialer{}

	// UDP: строим PacketProxy из нашего directUDPListener’а.
	udpListener := directUDPListener{}

	pp, err := network.NewPacketProxyFromPacketListener(udpListener)
	if err != nil {
		return nil, err
	}

	// Конфигурируем singleton-девайс lwIP:
	// TCP-трафик пойдёт через tcpDialer, UDP – через pp (наш plain UDP).
	// Возвращается network.IPDevice (Read/Write IP-пакеты).
	dev, err := lwip2transport.ConfigureDevice(tcpDialer, pp)
	if err != nil {
		return nil, err
	}

	return &DirectIPDevice{dev: dev}, nil
}

// Write – отправка IP-пакета в интернет.
// Используется твоим tunnel.readLoop() при RouteDirect.
func (d *DirectIPDevice) Write(pkt []byte) (int, error) {
	return d.dev.Write(pkt)
}

// Read – чтение IP-пакета из интернета.
// Используется твоим tunnel.directLoop().
func (d *DirectIPDevice) Read(buf []byte) (int, error) {
	return d.dev.Read(buf)
}

// Close – корректно закрывает lwIP-девайс.
func (d *DirectIPDevice) Close() error {
	return d.dev.Close()
}
