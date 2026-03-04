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
	// Логируем создание клиента, полезно знать, что конфиг дошел
	log.Infof("[Outline] Creating new client. Config length: %d", len(transportConfig))
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	log.Infof("[Outline] Connecting...")

	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("[Outline] Failed to create outline device: %v", err)
		return err
	}
	log.Infof("[Outline] Device created successfully")

	c.device = od
	common.Client.MarkActive(outlineCommon.Name)

	// Определяем функцию-диалер для TCP
	proxyDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Логгируем попытки соединения через прокси (осторожно, может быть спамно)
		log.Infof("[Outline] Dialing %s://%s", network, addr)
		return od.DialStream(ctx, addr)
	}

	log.Infof("[Outline] Starting Dobby TCP Tunnel...")

	// Передаем управление в tun2socks (gVisor)
	err = tunnel.StartDobbyTunnel(c.tun, proxyDialer)
	if err != nil {
		log.Infof("[Outline] Failed to start dobby tunnel: %v", err)
		return err
	}

	log.Infof("[Outline] Connect sequence completed successfully")
	return nil
}

func (c *OutlineClient) Disconnect() error {
	log.Infof("[Outline] Disconnecting...")

	tunnel.StopDobbyTunnel()

	if c.device != nil {
		log.Infof("[Outline] Closing device connection")
		c.device.Close()
	}

	common.Client.MarkInactive(outlineCommon.Name)
	log.Infof("[Outline] Disconnected")
	return nil
}

func (c *OutlineClient) Refresh() error {
	if c.device == nil {
		log.Infof("[Outline] Refresh called, but device is nil")
		return nil
	}
	log.Infof("[Outline] Refreshing device status")
	return c.device.Refresh()
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		log.Infof("[Outline] GetServerIP called, but device is nil")
		return nil
	}
	ip := c.device.GetServerIP()
	log.Infof("[Outline] Current server IP: %v", ip)
	return ip
}
