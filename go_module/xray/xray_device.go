package xray

import (
	"fmt"
	"net"

	"go_module/auth"
	log "go_module/log"
	"go_module/xray/internal"

	"github.com/xtls/xray-core/core"
)

type XrayDevice struct {
	xrayInstance *core.Instance
	vlessConfig  string
	proxyAddr    string
	svrIP        net.IP
	svrPort      int
	socksUser    string
	socksPass    string
}

func NewXrayDevice(vlessConfig string) (*XrayDevice, error) {
	serverIPStr, err := internal.ExtractServerIP(vlessConfig)
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

	socksUser := auth.GenerateRandomAuth()
	socksPass := auth.GenerateRandomAuth()

	d := &XrayDevice{
		xrayInstance: nil,
		vlessConfig:  vlessConfig,
		proxyAddr:    fmt.Sprintf("%s:%s@127.0.0.1:%d", socksUser, socksPass, port),
		svrIP:        ip.To4(),
		svrPort:      port,
		socksUser:    socksUser,
		socksPass:    socksPass,
	}

	log.Infof("[Xray] SOCKS bridge started at %s (serverIP=%s)", d.proxyAddr, d.svrIP.String())
	return d, nil
}

func (d *XrayDevice) Open(routingTableID int, uplinkIface string) error {
	xrayConfig, err := internal.GenerateXrayConfig(d.vlessConfig, "127.0.0.1", d.svrPort, routingTableID, uplinkIface, d.socksUser, d.socksPass)
	if err != nil {
		return fmt.Errorf("failed to generate xray config: %w", err)
	}

	d.xrayInstance, err = core.New(xrayConfig)
	if err != nil {
		return fmt.Errorf("failed to create xray instance: %w", err)
	}

	if err := d.xrayInstance.Start(); err != nil {
		_ = d.xrayInstance.Close()
		return fmt.Errorf("failed to start xray: %w", err)
	}

	loglevel, err := internal.ExtractLogLevel(d.vlessConfig)
	if err != nil {
		log.Infof("[Xray] failed to parse log level, continuing without logs")
	}
	internal.SetupXrayLogging(loglevel)

	return nil
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
