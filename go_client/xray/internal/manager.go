//go:build !(android || ios)
// +build !android,!ios

package internal

import (
	"fmt"
	"time"

	log "go_client/logger"

	"github.com/jackpal/gateway"
	"github.com/xtls/xray-core/core"
)

// PlatformConfigurator handles OS-specific interface and routing commands
type PlatformConfigurator interface {
	TunDeviceName() string
	SetupInterfaceAndRouting(serverIP, physGateway string) error
	TeardownRouting(serverIP, physGateway string)
}

type XrayManager struct {
	xrayInstance *core.Instance
	configRaw    string
	serverIP     string
	physGateway  string
	platform     PlatformConfigurator
}

func NewXrayManager(config string) *XrayManager {
	return &XrayManager{
		configRaw: config,
		platform:  newPlatformConfigurator(),
	}
}

func (m *XrayManager) Start() error {
	log.Infof("[Xray] Building Native TUN config...")

	tunDevice := m.platform.TunDeviceName()
	xrayConfig, err := GenerateXrayConfig(tunDevice, m.configRaw)
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

	loglevel, err := ExtractLogLevel(m.configRaw)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing without logs")
	}
	SetupXrayLogging(loglevel)

	var startErr error
	defer func() {
		if startErr != nil && m.xrayInstance != nil {
			_ = m.xrayInstance.Close()
			m.xrayInstance = nil
		}
	}()

	if startErr = m.xrayInstance.Start(); startErr != nil {
		return fmt.Errorf("failed to start xray: %w", startErr)
	}
	log.Infof("[Xray] Core started with Native TUN on %s", tunDevice)

	// Give the OS a moment to register the device if needed
	time.Sleep(500 * time.Millisecond)

	physGateway, err := gateway.DiscoverGateway()
	if err != nil {
		startErr = fmt.Errorf("failed to discover gateway: %w", err)
		return startErr
	}
	m.physGateway = physGateway.String()

	if startErr = m.platform.SetupInterfaceAndRouting(m.serverIP, m.physGateway); startErr != nil {
		return fmt.Errorf("failed to setup interface/routing: %w", startErr)
	}

	return nil
}

func (m *XrayManager) Stop() {
	if m.serverIP != "" && m.physGateway != "" {
		m.platform.TeardownRouting(m.serverIP, m.physGateway)
	}

	if m.xrayInstance != nil {
		if err := m.xrayInstance.Close(); err != nil {
			log.Infof("[Xray] Error closing instance: %v", err)
		}
		m.xrayInstance = nil
	}
}
