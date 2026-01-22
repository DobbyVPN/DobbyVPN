//go:build darwin
// +build darwin

package internal

import (
	"fmt"

	log "go_client/logger"
	"go_client/routing"
	xrayCommon "go_client/xray/common"

	"github.com/jackpal/gateway"
	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xtls/xray-core/core"
)

const (
	TunDevice  = "utun66" // Explicitly request utun66 or similar
	TunIP      = "10.0.85.2"
	TunGateway = "10.0.85.1"
	TunMask    = "255.255.255.0"
)

type XrayManager struct {
	xrayInstance *core.Instance
	tunEngine    *engine.Key
	configRaw    string
	serverIP     string
	physGateway  string
}

func NewXrayManager(config string) *XrayManager {
	return &XrayManager{
		configRaw: config,
	}
}

func (m *XrayManager) Start() error {
	// Start Xray Core
	log.Infof("[Xray] Building config...")
	xrayConfig, err := GenerateXrayConfig(xrayCommon.LocalSocksPort, m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to generate xray config: %w", err)
	}

	m.serverIP, err = ExtractServerIP(m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to extract server IP: %w", err)
	}

	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return fmt.Errorf("failed to create xray instance: %w", err)
	}

	if err := m.xrayInstance.Start(); err != nil {
		return fmt.Errorf("failed to start xray core: %w", err)
	}
	log.Infof("[Xray] Core started on 127.0.0.1:%d", xrayCommon.LocalSocksPort)

	// Start Tun2Socks
	// On macOS, "tun://utun66" attempts to create that specific interface.
	log.Infof("[Xray] Starting Tun2Socks engine...")
	key := &engine.Key{
		Device:     fmt.Sprintf("tun://%s", TunDevice),
		Proxy:      fmt.Sprintf("socks5://127.0.0.1:%d", xrayCommon.LocalSocksPort),
		LogLevel:   "info",
		UDPTimeout: 0,
	}
	engine.Insert(key)
	m.tunEngine = key

	go engine.Start()
	log.Infof("[Xray] Tun2Socks started on %s", TunDevice)

	// Configure Networking
	physGateway, err := gateway.DiscoverGateway()
	if err != nil {
		m.Stop()
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	m.physGateway = physGateway.String()

	// Assign IP to TUN interface
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ifconfig %s %s %s up", TunDevice, TunIP, TunGateway)); err != nil {
		m.Stop()
		return fmt.Errorf("failed to configure tun ip: %w", err)
	}

	// Apply System Routing
	err = routing.StartRouting(m.serverIP, m.physGateway, TunDevice)
	if err != nil {
		m.Stop()
		return fmt.Errorf("routing setup failed: %w", err)
	}

	return nil
}

func (m *XrayManager) Stop() {
	if m.serverIP != "" && m.physGateway != "" {
		routing.StopRouting(m.serverIP, m.physGateway)
	}

	if m.tunEngine != nil {
		engine.Stop()
	}

	if m.xrayInstance != nil {
		m.xrayInstance.Close()
	}
}
