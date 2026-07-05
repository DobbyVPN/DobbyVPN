package xray

import (
	"errors"
	"fmt"
	"net"

	"go_module/auth"
	"go_module/desktop_exports/common"
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

	// Pick a free local port for the SOCKS inbound. Xray opens TCP and UDP
	// on the same port when udp=true, so validate both protocols before
	// handing the port to xray.
	port, err := allocateLocalSocksPort()
	if err != nil {
		return nil, fmt.Errorf("failed to allocate local port: %w", err)
	}

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

	log.Debugf(common.Category, "SOCKS bridge started at %s (serverIP=%s)", d.proxyAddr, d.svrIP.String())
	return d, nil
}

func allocateLocalSocksPort() (int, error) {
	const attempts = 100

	var lastErr error = errors.New("no allocation attempts")
	for attempt := 1; attempt <= attempts; attempt++ {
		// Pick UDP first. On Windows a port can be free for TCP while UDP bind
		// is forbidden by an excluded/reserved range, which made Xray fail before
		// it even started. Xray needs both protocols on the same SOCKS port.
		pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			lastErr = err
			continue
		}

		port := pc.LocalAddr().(*net.UDPAddr).Port
		l, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			lastErr = err
			_ = pc.Close()
			continue
		}

		_ = l.Close()
		_ = pc.Close()
		if attempt > 1 {
			log.Debugf(common.Category, "Allocated Xray local SOCKS port after retries port=%d attempts=%d", port, attempt)
		}
		return port, nil
	}

	return 0, fmt.Errorf("no TCP/UDP localhost port available after %d attempts: %w", attempts, lastErr)
}

func (d *XrayDevice) Open(routingTableID int, uplinkIface string) error {
	if d == nil {
		return errors.New("xray device is not initialized")
	}

	loglevel, err := internal.ExtractLogLevel(d.vlessConfig)
	if err != nil {
		log.Debugf(common.Category, "failed to parse xray log level, using default=%s err=%v", internal.XrayLogLevelName(internal.DefaultXrayLogLevel()), err)
		loglevel = internal.DefaultXrayLogLevel()
	} else if loglevel == internal.NoXrayLogLevel() {
		log.Debugf(common.Category, "xray log level disabled in config, using default=%s for runtime diagnostics", internal.XrayLogLevelName(internal.DefaultXrayLogLevel()))
		loglevel = internal.DefaultXrayLogLevel()
	}
	internal.SetupXrayLogging(loglevel)

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

	return nil
}

func (d *XrayDevice) GetServerIP() net.IP {
	if d == nil {
		return nil
	}
	return d.svrIP
}

func (d *XrayDevice) GetProxyAddr() string {
	if d == nil {
		return ""
	}
	return d.proxyAddr
}

func (d *XrayDevice) Close() error {
	if d == nil {
		return errors.New("xray device is not initialized")
	}
	if d.xrayInstance != nil {
		err := d.xrayInstance.Close()
		d.xrayInstance = nil
		if err != nil {
			return fmt.Errorf("failed to close xray instance: %w", err)
		}
	}
	return nil
}
