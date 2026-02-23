//go:build linux && !android
// +build linux,!android

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
	TunDevice = "tun0"
	TunIP     = "10.0.85.2/24"
)

type XrayManager struct {
	xrayInstance *core.Instance
	configRaw    string
	serverIP     string
	physGateway  string
}

func NewXrayManager(config string) *XrayManager {
	return &XrayManager{configRaw: config}
}

func (m *XrayManager) Start() error {
	log.Infof("[Xray] Building Native TUN config...")

	// Generate Config asking Xray to create "tun0"
	xrayConfig, err := GenerateXrayConfig(TunDevice, m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	m.serverIP, err = ExtractServerIP(m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to extract server IP: %w", err)
	}

	// Start Xray (opens the TUN device)
	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return fmt.Errorf("failed to create xray: %w", err)
	}
	// Extract user's log level and set up logger
	loglevel, err := ExtractLogLevel(m.configRaw)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing without logs")
	}
	SetupXrayLogging(loglevel)
	defer func() {
		// Setting clean up if error occured
		if err != nil && m.xrayInstance != nil {
			_ = m.xrayInstance.Close()
			m.xrayInstance = nil
		}
	}()

	if err = m.xrayInstance.Start(); err != nil {
		return fmt.Errorf("failed to start xray: %w", err)
	}
	log.Infof("[Xray] Core started with Native TUN on %s", TunDevice)

	// Configure Networking
	// Give the OS a moment to register the device if needed (usually instant, but safe to yield)
	time.Sleep(500 * time.Millisecond)

	physGateway, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	m.physGateway = physGateway.String()

	// Assign IP to TUN interface
	// Xray creates the interface, but we must assign the IP/Mask
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip addr add %s dev %s", TunIP, TunDevice)); err != nil {
		return fmt.Errorf("failed to set tun ip: %w", err)
	}
	if _, err := routing.ExecuteCommand(fmt.Sprintf("sudo ip link set dev %s up", TunDevice)); err != nil {
		return fmt.Errorf("failed to up tun: %w", err)
	}

	// Apply System Routing
	err = routing.StartRouting(m.serverIP, m.physGateway, TunDevice)
	if err != nil {
		return fmt.Errorf("routing failed: %w", err)
	}

	return nil
}

func (m *XrayManager) Stop() {
	if m.serverIP != "" && m.physGateway != "" {
		routing.StopRouting(m.serverIP, m.physGateway)
	}

	if m.xrayInstance != nil {
		if err := m.xrayInstance.Close(); err != nil {
			log.Infof("[Xray] Error closing instance: %v", err)
		}
		m.xrayInstance = nil
	}
}
