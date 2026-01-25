//go:build windows

package internal

import (
	"fmt"
	"time"

	log "go_client/logger"
	"go_client/routing"
	xrayCommon "go_client/xray/common"

	// Xray
	"github.com/xtls/xray-core/core"

	// Tun2Socks
	"github.com/xjasonlyu/tun2socks/v2/engine"

	// Gateway
	"github.com/jackpal/gateway"
)

const (
	// Internal IP for the TUN adapter (similar to app.go)
	TunDeviceName = "wintun"
	TunIP         = "10.0.85.2"
	TunGateway    = "10.0.85.1"
	TunMask       = "255.255.255.0"
)

type XrayManager struct {
	xrayInstance  *core.Instance
	tunEngine     *engine.Key
	configRaw     string
	serverIP      string
	physGateway   string
	InterfaceName string
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
		return err
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
	log.Infof("[Xray] Starting Tun2Socks engine...")

	key := &engine.Key{
		Device:     fmt.Sprintf("tun://%s", TunDeviceName),
		Proxy:      fmt.Sprintf("socks5://127.0.0.1:%d", xrayCommon.LocalSocksPort),
		LogLevel:   "info",
		UDPTimeout: 0,
	}

	// This creates the interface "wintun" and starts the packet loop
	engine.Insert(key)

	go engine.Start()

	m.tunEngine = key
	log.Infof("[Xray] Tun2Socks started")

	// Configure Network & Routing

	// A. Find Physical Gateway
	// Wintun might take a few milliseconds to appear
	time.Sleep(500 * time.Millisecond)

	physicalGatewayIP, err := gateway.DiscoverGateway()
	if err != nil {
		return err
	}
	m.physGateway = physicalGatewayIP.String()

	log.Infof("[Routing] Discovering interfaces...")
	physicalInterfaceName, err := routing.FindInterfaceByGateway(m.physGateway)

	// Recover physical interface object
	netInterface, err := routing.GetNetworkInterfaceByIP(physicalInterfaceName)
	m.InterfaceName = netInterface.Name
	if err != nil {
		return fmt.Errorf("could not resolve physical interface: %v", err)
	}
	log.Infof("[Routing] Physical Interface: %s", m.InterfaceName)

	// B. Setup TUN IP
	// Since tun2socks created the device, we ensure IP is correct
	setIpCmd := fmt.Sprintf("netsh interface ip set address \"%s\" static %s %s %s", TunDeviceName, TunIP, TunMask, TunGateway)
	routing.ExecuteCommand(setIpCmd)

	// C. Set DNS
	setDnsCmd := fmt.Sprintf("netsh interface ip set dns \"%s\" static 8.8.8.8", TunDeviceName)
	routing.ExecuteCommand(setDnsCmd)

	// D. Extract TUN MAC
	tunInterface, err := routing.GetNetworkInterfaceByIP(TunIP)
	if err != nil {
		return fmt.Errorf("failed to find TUN interface after creation: %w", err)
	}

	// Spoof MAC logic for uniqueness
	dst := tunInterface.HardwareAddr
	spoofedMac := make([]byte, len(dst))
	copy(spoofedMac, dst)
	if len(spoofedMac) > 2 {
		spoofedMac[2] += 2
	}

	// D. Apply Routing Rules
	log.Infof("[Routing] Applying system routes...")
	err = routing.StartRouting(
		m.serverIP,
		m.physGateway,
		TunDeviceName,
		tunInterface.HardwareAddr.String(),
		m.InterfaceName,
		TunGateway,
		TunIP,
		spoofedMac,
	)

	if err != nil {
		m.Stop()
		return fmt.Errorf("routing setup failed: %w", err)
	}

	return nil
}

func (m *XrayManager) Stop() {
	log.Infof("[Xray] Stopping...")

	// 1. Stop Routing
	routing.StopRouting(m.serverIP, TunDeviceName, m.physGateway, m.InterfaceName)

	// 2. Stop Tun2Socks
	if m.tunEngine != nil {
		engine.Stop()
	}

	// 3. Stop Xray
	if m.xrayInstance != nil {
		m.xrayInstance.Close()
	}
}
