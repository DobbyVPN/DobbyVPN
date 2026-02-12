//go:build darwin
// +build darwin

package internal

import (
	"fmt"
	"time"

	log "go_client/logger"
	"go_client/routing"

	"github.com/jackpal/gateway"
	"github.com/xtls/xray-core/core"
)

const (
	TunDevice  = "utun66" // Xray will attempt to open this specific utun
	TunIP      = "10.0.85.2"
	TunGateway = "10.0.85.1"
)

type XrayManager struct {
	xrayInstance *core.Instance
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
	log.Infof("[Xray] Building Native TUN config...")

	// Generate Config asking Xray to create "utun66"
	xrayConfig, err := GenerateXrayConfig(TunDevice, m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to generate xray config: %w", err)
	}

	m.serverIP, err = ExtractServerIP(m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to extract server IP: %w", err)
	}

	// Start Xray (opens the utun device)
	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return fmt.Errorf("failed to create xray instance: %w", err)
	}
	// Extract user's log level and set up logger
	loglevel, err := ExtractLogLevel(m.configRaw)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing whithout logs")
	}
	SetupXrayLogging(loglevel)

	if err := m.xrayInstance.Start(); err != nil {
		return fmt.Errorf("failed to start xray core: %w", err)
	}
	log.Infof("[Xray] Core started with Native TUN on %s", TunDevice)

	// Configure Networking
	// Give the OS a moment to register the device if needed (usually instant, but safe to yield)
	time.Sleep(500 * time.Millisecond)

	physGateway, err := gateway.DiscoverGateway()
	if err != nil {
		m.Stop()
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	m.physGateway = physGateway.String()

	// Assign IP to TUN interface
	// macOS uses ifconfig for this usually.
	// Syntax: ifconfig <interface> <local_ip> <remote_ip> up
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

	if m.xrayInstance != nil {
		m.xrayInstance.Close()
	}
}
