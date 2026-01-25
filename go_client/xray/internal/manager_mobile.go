//go:build android || ios
// +build android ios

package internal

import (
	"fmt"

	log "go_client/logger"
	xrayCommon "go_client/xray/common"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"github.com/xtls/xray-core/core"
)

type XrayManager struct {
	xrayInstance *core.Instance
	tunEngine    *engine.Key
	configRaw    string
	tunFD        int
}

func NewXrayManager(config string, fd int) *XrayManager {
	return &XrayManager{configRaw: config, tunFD: fd}
}

func (m *XrayManager) Start() error {
	log.Infof("[Xray-Mobile] Starting...")

	// Start Xray Core
	xrayConfig, err := GenerateXrayConfig(xrayCommon.LocalSocksPort, m.configRaw)
	if err != nil {
		return err
	}
	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return err
	}
	if err := m.xrayInstance.Start(); err != nil {
		return err
	}

	// Start Tun2Socks using the File Descriptor
	key := &engine.Key{
		Device:     fmt.Sprintf("fd://%d", m.tunFD),
		Proxy:      fmt.Sprintf("socks5://127.0.0.1:%d", xrayCommon.LocalSocksPort),
		LogLevel:   "info",
		UDPTimeout: 0,
	}

	engine.Insert(key)
	m.tunEngine = key

	// Start the engine in a goroutine
	go engine.Start()

	log.Infof("[Xray-Mobile] Tun2Socks started on FD: %d", m.tunFD)

	return nil
}

func (m *XrayManager) Stop() {
	if m.tunEngine != nil {
		engine.Stop()
	}
	if m.xrayInstance != nil {
		m.xrayInstance.Close()
	}
	log.Infof("[Xray-Mobile] Stopped")
}
