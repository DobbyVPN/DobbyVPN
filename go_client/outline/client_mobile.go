//go:build android || ios

package outline

import (
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"go_client/tunnel"
	"io"
	"net"
)

type OutlineClient struct {
	device *internal.OutlineDevice
	tun    io.ReadWriteCloser
	config string
}

func NewClient(transportConfig string, tun io.ReadWriteCloser) *OutlineClient {
	c := &OutlineClient{
		config: transportConfig,
		tun:    tun,
	}
	log.Infof("outline client created")
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	// 1. Создаем устройство Outline (прокси)
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		return err
	}
	c.device = od

	// 2. Создаем функцию-диалер для прокси
	proxyDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Outline обычно предоставляет Dial через свой внутренний стек
		return od.Dial(network, addr)
	}

	// 3. Запускаем туннель с поддержкой геороутинга
	// Передаем c.tun (который io.ReadWriteCloser)
	err = tunnel.StartDobbyTunnel(c.tun, proxyDialer)
	if err != nil {
		return err
	}

	common.Client.MarkActive(outlineCommon.Name)
	log.Infof("Outline connected via Dobby Tunnel")
	return nil
}

func (c *OutlineClient) Disconnect() error {

	tunnel.StopDobbyTunnel()

	if c.device != nil {
		c.device.Close()
	}

	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	if c.device == nil {
		return nil
	}
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
