//go:build windows

package internal

import (
	"fmt"
	log "go_client/logger"
	"go_client/routing"
	"time"

	"github.com/jackpal/gateway"
	"github.com/xtls/xray-core/core"
)

const (
	TunDeviceName = "wintun" // Xray will create this interface
	TunIP         = "10.0.85.2"
	TunGateway    = "10.0.85.1"
	TunMask       = "255.255.255.0"
)

type XrayManager struct {
	xrayInstance  *core.Instance
	configRaw     string
	serverIP      string
	physGateway   string
	InterfaceName string
}

func NewXrayManager(config string) *XrayManager {
	return &XrayManager{configRaw: config}
}

func (m *XrayManager) Start() (err error) {
	log.Infof("[Xray] Building Native TUN config...")

	// Generate Config asking Xray to create "wintun"
	xrayConfig, err := GenerateXrayConfig(TunDeviceName, m.configRaw)
	if err != nil {
		return fmt.Errorf("failed to generate config: %w", err)
	}

	m.serverIP, err = ExtractServerIP(m.configRaw)
	if err != nil {
		return err
	}

	// Start Xray (creates the Wintun adapter)
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
	log.Infof("[Xray] Native TUN started on %s", TunDeviceName)

	// Configure Routing
	// Give the OS a moment to register the device if needed (usually instant, but safe to yield)
	time.Sleep(500 * time.Millisecond)

	physGateway, err := gateway.DiscoverGateway()
	if err != nil {
		return fmt.Errorf("failed to discover gateway: %w", err)
	}
	m.physGateway = physGateway.String()

	physInterface, err := routing.FindInterfaceByGateway(m.physGateway)
	if err != nil {
		return fmt.Errorf("failed to find network interface by gateway %s: %w", m.physGateway, err)
	}

	netInterface, err := routing.GetNetworkInterfaceByIP(physInterface)
	if err != nil {
		return fmt.Errorf("failed to resolve network interface for %s: %w", physInterface, err)
	}
	m.InterfaceName = netInterface.Name

	// Configure IP/DNS on the interface Xray just created
	if _, err = routing.ExecuteCommand(fmt.Sprintf("netsh interface ip set address \"%s\" static %s %s %s", TunDeviceName, TunIP, TunMask, TunGateway)); err != nil {
		return fmt.Errorf("failed to set TUN address: %w", err)
	}
	if _, err = routing.ExecuteCommand(fmt.Sprintf("netsh interface ip set dns \"%s\" static 8.8.8.8", TunDeviceName)); err != nil {
		return fmt.Errorf("failed to set TUN DNS: %w", err)
	}

	// Spoof MAC logic for uniqueness
	dst := netInterface.HardwareAddr
	var spoofedMac []byte
	// Check if MAC is empty (common with Wintun) and assign a dummy one
	if len(dst) == 0 {
		// Create a dummy private MAC (e.g., 02:00:00:00:00:01)
		spoofedMac = []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
		log.Infof("[Routing] Wintun has no MAC, using dummy: %v", spoofedMac)
	} else {
		spoofedMac = make([]byte, len(dst))
		copy(spoofedMac, dst)
		if len(spoofedMac) > 2 {
			spoofedMac[2] += 2
		}
	}

	// Apply Routes
	err = routing.StartRouting(
		m.serverIP,
		m.physGateway,
		TunDeviceName,
		dst.String(),
		m.InterfaceName,
		TunGateway,
		TunIP,
		spoofedMac,
	)

	return err
}

func (m *XrayManager) Stop() {
	// Clean up routing tables first
	routing.StopRouting(m.serverIP, TunDeviceName, m.physGateway, m.InterfaceName, TunGateway)

	if m.xrayInstance != nil {
		if err := m.xrayInstance.Close(); err != nil {
			log.Infof("[Xray] Error closing instance: %v", err)
		}
		m.xrayInstance = nil
	}
}
