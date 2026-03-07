//go:build android || ios

package outline

import (
	"fmt"
	"go_client/common"
	log "go_client/logger"
	outlineCommon "go_client/outline/common"
	"go_client/outline/internal"
	"go_client/tunnel"
	"io"
	"net"
	"os"
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
	log.Infof("outline client created (tun2socks version)")
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED от падения в Connect: %v", r)
		}
	}()
	// 1. Создаем устройство (теперь оно поднимает локальный SOCKS5 сервер)
	od, err := internal.NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("failed to create outline device: %v\n", err)
		return err
	}

	log.Infof("outline device (SOCKS5 bridge) created")
	c.device = od

	// 2. Получаем FD из интерфейса tun.
	// Обычно c.tun — это *os.File, переданный из NewOutlineClient (CGO)
	var fd int
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
	} else {
		// Если это не *os.File, нужно убедиться, как передается FD в NewClient
		log.Infof("failed to get FD from tun: not an *os.File")
		return fmt.Errorf("invalid tun device type")
	}

	// 3. Запускаем движок tun2socks
	log.Infof("starting tun2socks engine with proxy %s", od.GetProxyAddr())
	tunnel.StartEngine(fd, od.GetProxyAddr())

	common.Client.MarkActive(outlineCommon.Name)
	log.Infof("outline client connected successfully via tun2socks")
	return nil
}

func (c *OutlineClient) Disconnect() error {
	// 1. Останавливаем движок туннеля (закрывает стек и девайс)
	tunnel.StopEngine()

	// 2. Закрываем локальный SOCKS5 сервер
	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close outline device: %v\n", err)
		}
		c.device = nil
	}

	log.Infof("outline client disconnected")
	common.Client.MarkInactive(outlineCommon.Name)
	return nil
}

func (c *OutlineClient) Refresh() error {
	return nil
}

func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
