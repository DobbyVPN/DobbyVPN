//go:build android || ios

package outline

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"go_module/log"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"

	"golang.org/x/sys/unix"
)

// OutlineClient is a legacy mobile client used on Android when talking to
// liboutline directly (without the generic core.ProtocolDevice path).
// It owns the OutlineDevice instance and the tun2socks engine lifecycle.
type OutlineClient struct {
	device *OutlineDevice
	tun    io.ReadWriteCloser
	config string
}

// NewClient constructs a mobile Outline client bound to the given config and tun fd.
func NewClient(transportConfig string, tun io.ReadWriteCloser) *OutlineClient {
	c := &OutlineClient{
		config: transportConfig,
		tun:    tun,
	}
	log.Infof("outline client created (tun2socks version) config.len=%d tunType=%T", len(transportConfig), tun)
	return c
}

// Connect resolves the Outline transport, starts the SOCKS5 bridge and tun2socks.
func (c *OutlineClient) Connect() error {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			log.Infof("RECOVERED from fail in Connect after %s: %v", time.Since(start), r)
		}
	}()

	log.Infof("outline Connect begin config.len=%d tunType=%T", len(c.config), c.tun)

	od, err := NewOutlineDevice(c.config)
	if err != nil {
		log.Infof("failed to create outline device after %s: %v", time.Since(start), err)
		return err
	}

	log.Infof("outline device (SOCKS5 bridge) created proxy=%s serverIP=%v elapsed=%s", od.GetProxyAddr(), od.GetServerIP(), time.Since(start))
	c.device = od

	var fd int
	if f, ok := c.tun.(*os.File); ok {
		fd = int(f.Fd())
		if err := unix.SetNonblock(fd, true); err != nil {
			log.Infof("Set unix.SetNonblock error: %v", err)
		} else {
			log.Infof("[DEBUG][Core] tun fd set non-blocking fd=%d", fd)
		}
	} else {
		log.Infof("failed to get FD from tun: not an *os.File")
		return fmt.Errorf("invalid tun device type")
	}

	log.Infof("starting tun2socks engine with proxy %s fd=%d", od.GetProxyAddr(), fd)
	if err := tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   od.GetProxyAddr(),
		FD:          fd,
		UplinkIface: "",
	}); err != nil {
		log.Infof("Can't start tun2socks after %s: %v", time.Since(start), err)
		return err
	}

	log.Infof("outline client connected successfully via tun2socks elapsed=%s", time.Since(start))
	return nil
}

// Disconnect stops tun2socks and closes the Outline device.
func (c *OutlineClient) Disconnect() error {
	start := time.Now()
	log.Infof("outline Disconnect begin deviceNil=%v", c.device == nil)
	log.Infof("outline Disconnect stopping tun2socks engine")
	tunnel.StopEngine()
	log.Infof("outline Disconnect tun2socks engine stopped elapsed=%s", time.Since(start))

	if c.device != nil {
		log.Infof("outline Disconnect closing outline device proxy=%s", c.device.GetProxyAddr())
		if err := c.device.Close(); err != nil {
			log.Infof("failed to close outline device: %v", err)
		}
		c.device = nil
	}

	log.Infof("outline client disconnected elapsed=%s", time.Since(start))
	return nil
}

// Refresh is a no-op for the legacy mobile client.
func (c *OutlineClient) Refresh() error {
	return nil
}

// GetServerIP returns the resolved server IP.
func (c *OutlineClient) GetServerIP() net.IP {
	if c.device == nil {
		return nil
	}
	return c.device.GetServerIP()
}
