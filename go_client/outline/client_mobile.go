//go:build android || ios

package outline

import (
	"context"
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
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("failed to create outline device: %v", err)
		return err
	}

	c.device = od
	common.Client.MarkActive(outlineCommon.Name)

	// Определяем функцию-диалер для TCP
	proxyDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Вызываем метод DialStream, который мы прописали в OutlineDevice
		return od.DialStream(ctx, addr)
	}

	log.Infof("Starting Dobby TCP Tunnel...")

	// Передаем управление в tun2socks (gVisor)
	err = tunnel.StartDobbyTunnel(c.tun, proxyDialer)
	if err != nil {
		log.Infof("failed to start dobby tunnel: %v", err)
		return err
	}

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
