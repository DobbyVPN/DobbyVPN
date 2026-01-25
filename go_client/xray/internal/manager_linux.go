//go:build linux && !android
// +build linux,!android

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
	TunDevice = "tun0"
	TunIP     = "10.0.85.2/24"
)

type XrayManager struct {
	xrayInstance *core.Instance
	tunEngine    *engine.Key
	configRaw    string
	serverIP     string
	physGateway  string
}

func NewXrayManager(config string) *XrayManager {
	return &XrayManager{configRaw: config}
}

func (m *XrayManager) Start() error {
	// Start Xray Core
	log.Infof("[Xray] Building config...")
	xrayConfig, err := GenerateXrayConfig(xrayCommon.LocalSocksPort, m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	m.serverIP, err = ExtractServerIP(m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to extract server IP: %w", err)
	}

	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return fmt.Errorf("failed to create xray: %w", err)
	}
	if err := m.xrayInstance.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %w", err)
	}
	log.Infof("[Xray] Core started on 127.0.0.1:%d", xrayCommon.LocalSocksPort)

	// Start Tun2Socks
	log.Infof("[Xray] Starting Tun2Socks engine...")
	key := &engine.Key{
		Device:   fmt.Sprintf("tun://%s", TunDevice),
		Proxy:    fmt.Sprintf("socks5://127.0.0.1:%d", xrayCommon.LocalSocksPort),
		LogLevel: "info",
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
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip addr add %s dev %s", TunIP, TunDevice)); err != nil {
		m.Stop()
		return fmt.Errorf("failed to set tun ip: %w", err)
	}
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip link set dev %s up", TunDevice)); err != nil {
		m.Stop()
		return fmt.Errorf("failed to up tun: %w", err)
	}

	// Apply System Routing
	err = routing.StartRouting(m.serverIP, m.physGateway, TunDevice)
	if err != nil {
		m.Stop()
		return fmt.Errorf("routing failed: %w", err)
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
