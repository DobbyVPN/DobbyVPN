//go:build android || ios

package outline

import (
	"fmt"
	"go_module/common"
	"go_module/log"
	outlineCommon "go_module/outline/common"
	"go_module/outline/internal"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"golang.org/x/sys/unix"
	"io"
	"net"
	"os"
	"time"
)

type OutlineClient struct {
	device        *internal.OutlineDevice
	tunFD         int
	engineStarted bool
	config        string
	mtu           int
	options       ClientOptions
}

type ClientOptions struct {
	PreferTCPDNSForWebSocket bool
	DisableNonDNSUDP         bool
}

func NewClient(transportConfig string, tun io.ReadWriteCloser) *OutlineClient {
	return NewClientWithMTU(transportConfig, tun, 0)
}

func NewClientWithMTU(transportConfig string, tun io.ReadWriteCloser, mtu int) *OutlineClient {
	fd := -1
	if f, ok := tun.(*os.File); ok {
		fd = int(f.Fd())
	} else {
		log.Infof("failed to get FD from tun: not an *os.File")
	}
	return NewClientWithFD(transportConfig, fd, mtu)
}

func NewClientWithFD(transportConfig string, fd int, mtu int) *OutlineClient {
	return NewClientWithFDAndOptions(transportConfig, fd, mtu, ClientOptions{})
}

func NewClientWithFDAndOptions(transportConfig string, fd int, mtu int, options ClientOptions) *OutlineClient {
	if mtu <= 0 {
		mtu = 1200
	}
	c := &OutlineClient{
		config:  transportConfig,
		tunFD:   fd,
		mtu:     mtu,
		options: options,
	}
	log.Infof(
		"outline client created (tun2socks version) fd=%d mtu=%d configLen=%d preferTCPDNSForWebSocket=%v disableNonDNSUDP=%v",
		fd,
		mtu,
		len(transportConfig),
		options.PreferTCPDNSForWebSocket,
		options.DisableNonDNSUDP,
	)
	common.Client.SetVpnClient(outlineCommon.Name, c)
	return c
}

func (c *OutlineClient) Connect() error {
	start := time.Now()
	log.Infof("outline client connect begin fd=%d mtu=%d configLen=%d", c.tunFD, c.mtu, len(c.config))
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED from fail in Connect: %v", r)
		}
	}()
	od, err := internal.NewOutlineDeviceWithOptions(c.config, internal.DeviceOptions{
		PreferTCPDNSForWebSocket: c.options.PreferTCPDNSForWebSocket,
		DisableNonDNSUDP:         c.options.DisableNonDNSUDP,
	})
	if err != nil {
		log.Infof("failed to create outline device: %v\n", err)
		return err
	}

	log.Infof("outline device (SOCKS5 bridge) created")
	c.device = od

	fd := c.tunFD
	if fd < 0 {
		return fmt.Errorf("invalid tun fd")
	}
	err = unix.SetNonblock(fd, true)
	if err != nil {
		log.Infof("Set unix.SetNonblock error: %v", err)
	} else {
		log.Infof("[DEBUG][Outline] tun fd set non-blocking fd=%d", fd)
	}

	log.Infof("starting tun2socks engine with proxy %s fd=%d mtu=%d", od.GetProxyAddr(), fd, c.mtu)
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   od.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
		MTU:         c.mtu,
	})
	if err != nil {
		log.Infof("Can't start tun2socks: %v", err)
		return err
	}
	c.engineStarted = true

	common.Client.MarkActive(outlineCommon.Name)
	log.Infof("outline client connected successfully via tun2socks in %dms", time.Since(start).Milliseconds())
	return nil
}

func (c *OutlineClient) Disconnect() error {
	log.Infof("outline client disconnect begin engineStarted=%v fd=%d deviceNil=%v", c.engineStarted, c.tunFD, c.device == nil)
	tunnel.StopEngine()
	if !c.engineStarted && c.tunFD >= 0 {
		if err := unix.Close(c.tunFD); err != nil {
			log.Infof("failed to close unused tun fd: %v\n", err)
		} else {
			log.Infof("[DEBUG][Outline] closed unused tun fd=%d", c.tunFD)
		}
	}
	c.engineStarted = false
	c.tunFD = -1

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

func (c *OutlineClient) Status() string {
	if c == nil {
		return "client=false engineStarted=false fd=-1 deviceNil=true localProxyAlive=false reason=client_nil"
	}
	if c.device == nil {
		return fmt.Sprintf(
			"client=true engineStarted=%v fd=%d deviceNil=true localProxyAlive=false reason=device_nil",
			c.engineStarted,
			c.tunFD,
		)
	}
	return fmt.Sprintf(
		"client=true engineStarted=%v fd=%d deviceNil=false %s",
		c.engineStarted,
		c.tunFD,
		c.device.Status(750*time.Millisecond),
	)
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
