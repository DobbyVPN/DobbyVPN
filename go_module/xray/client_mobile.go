//go:build android || ios
// +build android ios

package xray

import (
	"sync"

	"go_module/common"
	log "go_module/log"
	"go_module/tunnel"
	"go_module/tunnel/platform_engine"
	xrayCommon "go_module/xray/common"
	"go_module/xray/internal"
)

type XrayClient struct {
	device *internal.XrayDevice
	config string
	mu     sync.Mutex
	fd     int
}

func NewXrayClient(vlessConfig string, fileDescriptor int) *XrayClient {
	client := &XrayClient{
		config: vlessConfig,
		fd:     fileDescriptor,
	}
	common.Client.SetVpnClient(xrayCommon.Name, client)
	return client
}

func (c *XrayClient) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("XrayClient: Connecting...")

	if c.device != nil {
		log.Infof("XrayClient: already connected; skipping Connect()")
		return nil
	}

	device, err := internal.NewXrayDevice(c.config, 0, "")
	if err != nil {
		log.Infof("XrayClient: failed to create xray device: %v", err)
		common.Client.MarkInactive(xrayCommon.Name)
		return err
	}

	err = tunnel.StartEngine(platform_engine.EngineConfig{
		ProxyAddr:   device.GetProxyAddr(),
		FD:          c.fd,
		UplinkIface: "",
	})
	if err != nil {
		_ = device.Close()
		common.Client.MarkInactive(xrayCommon.Name)
		return err
	}

	c.device = device

	common.Client.MarkActive(xrayCommon.Name)
	log.Infof("XrayClient: Connected successfully.")
	return nil
}

func (c *XrayClient) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	log.Infof("XrayClient: Disconnecting...")

	tunnel.StopEngine()

	if c.device != nil {
		_ = c.device.Close()
		c.device = nil
	}

	common.Client.MarkInactive(xrayCommon.Name)
	return nil
}

func (c *XrayClient) Refresh() error {
	_ = c.Disconnect()
	return c.Connect()
}
