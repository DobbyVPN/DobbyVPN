package internal

import (
	"fmt"
	"net"

	log "go_module/log"

	"github.com/xtls/xray-core/core"
)

type XrayDevice struct {
	xrayInstance *core.Instance
	proxyAddr    string
	svrIP        net.IP
}

func NewXrayDevice(vlessConfig string, routingTableID int, uplinkIface string) (*XrayDevice, error) {
	serverIPStr, err := ExtractServerIP(vlessConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to extract server IP: %w", err)
	}

	ip := net.ParseIP(serverIPStr)
	if ip == nil {
		return nil, fmt.Errorf("invalid server IP: %q", serverIPStr)
	}

	// Pick a free local port for the SOCKS inbound.
	// We intentionally bind+close to reserve a likely-free port for xray to listen on.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to allocate local port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()

	xrayConfig, err := GenerateXrayConfig(vlessConfig, "127.0.0.1", port, routingTableID, uplinkIface)
	if err != nil {
		return nil, fmt.Errorf("failed to generate xray config: %w", err)
	}

	xrayInstance, err := core.New(xrayConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create xray instance: %w", err)
	}

	loglevel, err := ExtractLogLevel(vlessConfig)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing without logs")
	}
	SetupXrayLogging(loglevel)

	if err := xrayInstance.Start(); err != nil {
		_ = xrayInstance.Close()
		return nil, fmt.Errorf("failed to start xray: %w", err)
	}

	d := &XrayDevice{
		xrayInstance: xrayInstance,
		proxyAddr:    fmt.Sprintf("127.0.0.1:%d", port),
		svrIP:        ip.To4(),
	}

	log.Infof("[Xray] SOCKS bridge started at %s (serverIP=%s)", d.proxyAddr, d.svrIP.String())
	return d, nil
}

func (d *XrayDevice) GetServerIP() net.IP {
	return d.svrIP
}

func (d *XrayDevice) GetProxyAddr() string {
	return d.proxyAddr
}

func (d *XrayDevice) Close() error {
	if d.xrayInstance != nil {
		err := d.xrayInstance.Close()
		d.xrayInstance = nil
		return err
	}
	return nil
}
