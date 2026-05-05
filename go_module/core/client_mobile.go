//go:build android || ios

package core

import (
	"fmt"
	"go_module/common"
	coreCommon "go_module/core/common"
	"go_module/core/pkg"
	"go_module/log"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

type CoreClient struct {
	device        pkg.ProtocolDevice
	tunFD         int
	engineStarted bool
	mtu           int
}

type ClientOptions struct {
	PreferTCPDNSForWebSocket bool
}

// NewClient creates a CoreClient with the given protocol device and TUN file descriptor
func NewClient(device pkg.ProtocolDevice, tunFD int, mtu int) *CoreClient {
	if mtu <= 0 {
		mtu = 1200
	}
	c := &CoreClient{
		device: device,
		tunFD:  tunFD,
		mtu:    mtu,
	}
	log.Infof("core mobile client created (tun2socks version) fd=%d mtu=%d", tunFD, mtu)
	common.Client.SetVpnClient(coreCommon.Name, c)
	return c
}

func (c *CoreClient) Connect() error {
	start := time.Now()
	log.Infof("core client connect begin fd=%d mtu=%d", c.tunFD, c.mtu)
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED from fail in Connect: %v", r)
		}
	}()

	fd := c.tunFD
	if fd < 0 {
		return fmt.Errorf("invalid tun fd")
	}
	err := unix.SetNonblock(fd, true)
	if err != nil {
		log.Infof("Set unix.SetNonblock error: %v", err)
	} else {
		log.Infof("[DEBUG][Core] tun fd set non-blocking fd=%d", fd)
	}

	err = c.device.Open(0, "")
	if err != nil {
		log.Infof("failed to create protocol device: %v", err)
		common.Client.MarkInactive(coreCommon.Name)
		return err
	}

	log.Infof("starting tun2socks engine with proxy %s fd=%d mtu=%d", c.device.GetProxyAddr(), fd, c.mtu)
	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   c.device.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
		MTU:         c.mtu,
	})
	if err != nil {
		log.Infof("Can't start tun2socks: %v", err)
		return err
	}
	c.engineStarted = true

	common.Client.MarkActive(coreCommon.Name)
	log.Infof("core client connected successfully via tun2socks in %dms", time.Since(start).Milliseconds())
	return nil
}

func (c *CoreClient) Disconnect() error {
	log.Infof("core client disconnect begin engineStarted=%v fd=%d deviceNil=%v", c.engineStarted, c.tunFD, c.device == nil)
	tunnel.StopEngine()
	if !c.engineStarted && c.tunFD >= 0 {
		if err := unix.Close(c.tunFD); err != nil {
			log.Infof("failed to close unused tun fd: %v\n", err)
		} else {
			log.Infof("[DEBUG][Core] closed unused tun fd=%d", c.tunFD)
		}
	}
	c.engineStarted = false
	c.tunFD = -1

	if c.device != nil {
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close protocol device: %v\n", err)
		}
		c.device = nil
	}

	log.Infof("core client disconnected")
	common.Client.MarkInactive(coreCommon.Name)
	return nil
}

func (c *CoreClient) Status() string {
	if c == nil {
		return "client=false engineStarted=false fd=-1 deviceNil=true reason=client_nil"
	}
	if c.device == nil {
		return fmt.Sprintf(
			"client=true engineStarted=%v fd=%d deviceNil=true reason=device_nil",
			c.engineStarted,
			c.tunFD,
		)
	}
	base := fmt.Sprintf(
		"client=true engineStarted=%v fd=%d deviceNil=false serverIP=%s",
		c.engineStarted,
		c.tunFD,
		c.device.GetServerIP().String(),
	)
	// If the device exposes local-proxy liveness, include it so that the
	// health-check layer (HealthCheckImpl.isOutlineProxyAliveViaXPC) can
	// detect localProxyAlive=true/false via the XPC provider message.
	if sp, ok := c.device.(pkg.ProxyStatusProvider); ok {
		proxyStatus := sp.Status(2 * time.Second)
		base = base + " " + proxyStatus
	}
	return base
}

func (c *CoreClient) Refresh() error {
	return nil
}

func (c *CoreClient) GetServerIP() net.IP {
	return c.device.GetServerIP()
}
