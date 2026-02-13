//go:build android || ios
// +build android ios

package internal

import (
	"fmt"
	log "go_client/logger"
	"os"
	"strconv"

	"github.com/xtls/xray-core/core"
)

type XrayManager struct {
	xrayInstance *core.Instance
	configRaw    string
	tunFD        int
}

func NewXrayManager(config string, fd int) *XrayManager {
	return &XrayManager{configRaw: config, tunFD: fd}
}

func (m *XrayManager) Start() error {
	log.Infof("[Xray-Mobile] Starting Native TUN...")

	// Set the Environment Variable as required by Xray documentation
	if err := os.Setenv("xray.tun.fd", strconv.Itoa(m.tunFD)); err != nil {
		return fmt.Errorf("failed to set tun fd env: %w", err)
	}

	// Pass the FD as the interface name in format "fd://1234"
	tunName := fmt.Sprintf("fd://%d", m.tunFD)

	xrayConfig, err := GenerateXrayConfig(tunName, m.configRaw)
	if err != nil {
		return err
	}

	m.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return err
	}
	// Extract user's log level and set up logger
	loglevel, err := ExtractLogLevel(m.configRaw)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing whithout logs")
	}
	SetupXrayLogging(loglevel)

	if err := m.xrayInstance.Start(); err != nil {
		return err
	}

	log.Infof("[Xray-Mobile] Started using FD: %d", m.tunFD)
	return nil
}

func (m *XrayManager) Stop() {
	if m.xrayInstance != nil {
		m.xrayInstance.Close()
	}
	log.Infof("[Xray-Mobile] Stopped")
}
