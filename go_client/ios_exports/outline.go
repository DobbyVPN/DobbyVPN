package cloak_outline

import (
	"context"
	"errors"
	"fmt"
	"net"

	"go_client/outline/configutil"

	"github.com/Jigsaw-Code/outline-sdk/network"
	"github.com/Jigsaw-Code/outline-sdk/network/lwip2transport"
	"github.com/Jigsaw-Code/outline-sdk/transport"
	"github.com/Jigsaw-Code/outline-sdk/x/configurl"
)

const (
	connectivityTestDomain   = "www.google.com"
	connectivityTestResolver = "1.1.1.1:53"
)

type OutlineDevice struct {
	network.IPDevice
	sd    transport.StreamDialer
	pp    *outlinePacketProxy
	svrIP net.IP
}

var configProviders = configurl.NewDefaultProviders()

func NewOutlineDevice(transportConfig string) (od *OutlineDevice, err error) {
	normalizedConfig, err := configutil.NormalizeTransportConfig(transportConfig)
	if err != nil {
		return nil, err
	}

	ip, err := resolveShadowsocksServerIPFromConfig(normalizedConfig)
	if err != nil {
		return nil, err
	}
	od = &OutlineDevice{
		svrIP: ip,
	}

	if od.sd, err = configProviders.NewStreamDialer(context.Background(), normalizedConfig); err != nil {
		return nil, fmt.Errorf("failed to create TCP dialer: %w", err)
	}

	if od.pp, err = newOutlinePacketProxy(normalizedConfig); err != nil {
		return nil, fmt.Errorf("failed to create delegate UDP proxy: %w", err)
	}

	if od.IPDevice, err = lwip2transport.ConfigureDevice(od.sd, od.pp); err != nil {
		return nil, fmt.Errorf("failed to configure lwIP: %w", err)
	}

	return od, nil
}

func (d *OutlineDevice) Close() error {
	return d.IPDevice.Close()
}

func (d *OutlineDevice) Refresh() error {
	return d.pp.testConnectivityAndRefresh(connectivityTestResolver, connectivityTestDomain)
}

func (d *OutlineDevice) GetServerIP() net.IP {
	return d.svrIP
}

func (d *OutlineDevice) Read() ([]byte, error) {
	buf := make([]byte, 65536)
	n, err := d.IPDevice.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}
	return buf[:n], nil
}

func (d *OutlineDevice) Write(buf []byte) (int, error) {
	n, err := d.IPDevice.Write(buf)
	if err != nil {
		return 0, fmt.Errorf("failed to write data: %w", err)
	}
	return n, nil
}

func resolveShadowsocksServerIPFromConfig(transportConfig string) (net.IP, error) {
	host, err := configutil.ExtractShadowsocksHost(transportConfig)
	if err != nil {
		return nil, err
	}
	ipList, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("invalid server hostname: %w", err)
	}

	// Мы поддерживаем только IPv4 в текущем коде
	for _, ip := range ipList {
		if ip = ip.To4(); ip != nil {
			return ip, nil
		}
	}
	return nil, errors.New("IPv6 only Shadowsocks server is not supported yet")
}
