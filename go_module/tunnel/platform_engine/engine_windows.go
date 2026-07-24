//go:build windows && !(android || ios)

package platform_engine

import (
	"fmt"
	"go_module/common"
	"net"
	"strings"
	"time"
	"unsafe"

	"go_module/log"
	"go_module/routing"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"golang.org/x/sys/windows"
)

var (
	lastIface string
	prevDNS   []string
	prevDHCP  bool
	prevDAD   uint32
	dadKnown  bool
)

const (
	windowsTunMTU      = 1200
	WindowsAdapterName = "wintun"
)

func execAndLog(cmd string, context string) error {
	startedAt := time.Now()
	out, err := routing.ExecuteCommand(cmd)
	elapsed := time.Since(startedAt).Truncate(time.Millisecond)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows][ERROR] %s elapsed=%s: %v | output=%s",
			context, elapsed, err, out,
		)
		return err
	}

	log.Debugf(Category, "[Engine][Windows][OK] %s elapsed=%s: %s", context, elapsed, out)
	return nil
}

func startPlatformEngine(cfg interface{}) error {
	startedAt := time.Now()
	c := cfg.(EngineConfig)
	proxyAddr := c.ProxyAddr
	uplinkIface := c.UplinkIface

	log.Debugf(Category, "[Engine][Windows] proxy_ready=true uplink_iface=%s", uplinkIface)
	if routing.IsTunnelInterfaceName(uplinkIface) {
		return fmt.Errorf("refusing to use tunnel interface %q as Windows uplink", uplinkIface)
	}

	key := &engine.Key{
		Proxy:     fmt.Sprintf("socks5://%s", proxyAddr),
		Device:    WindowsAdapterName,
		Interface: uplinkIface,
		LogLevel:  "info",
		MTU:       windowsTunMTU,
	}

	engine.Insert(key)
	engineStartAt := time.Now()
	engine.Start()
	log.Debugf(Category, "[Engine][Windows] engine.Start returned elapsed=%s total=%s", time.Since(engineStartAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	waitStartedAt := time.Now()
	ifName, err := waitForWintun(5 * time.Second)
	if err != nil {
		engine.Stop()
		return err
	}
	log.Debugf(Category, "[Engine][Windows] waitForWintun OK iface=%s elapsed=%s total=%s", ifName, time.Since(waitStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	lastIface = ifName
	previousDAD, err := getInterfaceDADTransmits(ifName)
	if err != nil {
		lastIface = ""
		engine.Stop()
		return err
	}
	prevDAD, dadKnown = previousDAD, true
	if previousDAD != 0 {
		if err := setInterfaceDADTransmits(ifName, 0); err != nil {
			stopPlatformEngine()
			engine.Stop()
			return err
		}
	}

	dnsReadStartedAt := time.Now()
	prevDNS, prevDHCP = getCurrentDNS(ifName)
	log.Debugf(Category, "[Engine][Windows] getCurrentDNS elapsed=%s total=%s", time.Since(dnsReadStartedAt).Truncate(time.Millisecond), time.Since(startedAt).Truncate(time.Millisecond))

	tunCfg := common.GetNetworkConfig()

	if err := setInterfaceAddress(ifName, tunCfg.TunDevice); err != nil {
		stopPlatformEngine()
		engine.Stop()
		return err
	}
	if err := waitForPreferredIPv4(ifName, tunCfg.TunDevice, 5*time.Second); err != nil {
		stopPlatformEngine()
		engine.Stop()
		return err
	}
	if err := setDNS(ifName, "1.1.1.1"); err != nil {
		stopPlatformEngine()
		engine.Stop()
		return err
	}

	log.Debugf(Category, "[Engine][Windows] platform engine ready iface=%s elapsed=%s", ifName, time.Since(startedAt).Truncate(time.Millisecond))
	return nil
}

func stopPlatformEngine() {
	if lastIface == "" {
		return
	}

	log.Debugf(Category, "[Engine][Windows] Restoring DNS. DHCP=%v DNS=%v", prevDHCP, prevDNS)

	if prevDHCP {
		cmd := fmt.Sprintf(
			"netsh interface ipv4 set dnsservers name=\"%s\" dhcp",
			lastIface,
		)
		_ = execAndLog(cmd, "restore DNS (DHCP)")

	} else if len(prevDNS) > 0 {

		// primary
		cmd := fmt.Sprintf(
			"netsh interface ipv4 set dnsservers name=\"%s\" static %s primary",
			lastIface, prevDNS[0],
		)
		_ = execAndLog(cmd, "restore DNS primary")

		// additional
		for i := 1; i < len(prevDNS); i++ {
			cmd := fmt.Sprintf(
				"netsh interface ipv4 add dnsservers name=\"%s\" %s index=%d",
				lastIface, prevDNS[i], i+1,
			)
			_ = execAndLog(cmd, fmt.Sprintf("restore DNS index=%d", i+1))
		}
	}
	if dadKnown {
		log.Debugf(Category, "[Engine][Windows] restoring DAD transmits iface=%s count=%d", lastIface, prevDAD)
		_ = setInterfaceDADTransmits(lastIface, prevDAD)
	}
	lastIface = ""
	prevDNS = nil
	prevDHCP = false
	prevDAD = 0
	dadKnown = false
}

func waitForWintun(timeout time.Duration) (string, error) {
	iface, err := routing.WaitForInterfaceName(WindowsAdapterName, timeout)
	if err != nil {
		return "", fmt.Errorf("owned Wintun adapter %q not found: %w", WindowsAdapterName, err)
	}
	return iface.Name, nil
}

func windowsSetDADCommand(name string, transmits uint32) string {
	return fmt.Sprintf(
		"netsh interface ipv4 set interface interface=\"%s\" dadtransmits=%d store=active",
		name,
		transmits,
	)
}

func getInterfaceDADTransmits(name string) (uint32, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return 0, fmt.Errorf("resolve interface %q for DAD snapshot: %w", name, err)
	}
	row := windows.MibIpInterfaceRow{
		Family:         windows.AF_INET,
		InterfaceIndex: uint32(iface.Index),
	}
	if err := windows.GetIpInterfaceEntry(&row); err != nil {
		return 0, fmt.Errorf("read interface %q DAD settings: %w", name, err)
	}
	return row.DadTransmits, nil
}

func setInterfaceDADTransmits(name string, transmits uint32) error {
	return execAndLog(windowsSetDADCommand(name, transmits), "setInterfaceDADTransmits")
}

func waitForPreferredIPv4(name, expectedAddress string, timeout time.Duration) error {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return fmt.Errorf("resolve interface %q for IPv4 readiness: %w", name, err)
	}
	expected := net.ParseIP(expectedAddress).To4()
	if expected == nil {
		return fmt.Errorf("parse expected IPv4 address for interface %q", name)
	}
	startedAt := time.Now()
	deadline := startedAt.Add(timeout)
	for {
		found, preferred, skipAsSource, duplicate, err := interfaceIPv4State(uint32(iface.Index), expected)
		if err != nil {
			return fmt.Errorf("read IPv4 readiness for interface %q: %w", name, err)
		}
		if found && preferred && !skipAsSource {
			log.Debugf(
				Category,
				"[Engine][Windows] IPv4 source ready iface=%s elapsed=%s",
				name,
				time.Since(startedAt).Truncate(time.Millisecond),
			)
			return nil
		}
		if duplicate {
			return fmt.Errorf("IPv4 address for interface %q is duplicate", name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf(
				"IPv4 address for interface %q not preferred after %s found=%t skipAsSource=%t",
				name,
				time.Since(startedAt).Truncate(time.Millisecond),
				found,
				skipAsSource,
			)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func interfaceIPv4State(interfaceIndex uint32, expected net.IP) (found, preferred, skipAsSource, duplicate bool, err error) {
	var table *windows.MibUnicastIpAddressTable
	if err = windows.GetUnicastIpAddressTable(windows.AF_INET, &table); err != nil {
		return
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))
	rows := unsafe.Slice(&table.Table[0], table.NumEntries)
	return findInterfaceIPv4State(rows, interfaceIndex, expected)
}

func findInterfaceIPv4State(rows []windows.MibUnicastIpAddressRow, interfaceIndex uint32, expected net.IP) (found, preferred, skipAsSource, duplicate bool, err error) {
	expected = expected.To4()
	if expected == nil {
		return false, false, false, false, fmt.Errorf("expected address is not IPv4")
	}
	for index := range rows {
		row := &rows[index]
		if row.InterfaceIndex != interfaceIndex || row.Address.Family != windows.AF_INET {
			continue
		}
		raw := (*windows.RawSockaddrInet4)(unsafe.Pointer(&row.Address))
		if !net.IP(raw.Addr[:]).Equal(expected) {
			continue
		}
		return true,
			row.DadState == windows.IpDadStatePreferred,
			row.SkipAsSource != 0,
			row.DadState == windows.IpDadStateDuplicate,
			nil
	}
	return false, false, false, false, nil
}

func setInterfaceAddress(name, ip string) error {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 set address name=\"%s\" source=static addr=%s mask=255.255.255.0",
		name, ip,
	)
	return execAndLog(cmd, "setInterfaceAddress")
}

func setDNS(name, dns string) error {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 set dnsservers name=\"%s\" static %s",
		name, dns,
	)
	return execAndLog(cmd, "setDNS")
}

func getCurrentDNS(name string) ([]string, bool) {
	cmd := fmt.Sprintf(
		"netsh interface ipv4 show dnsservers name=\"%s\"",
		name,
	)

	out, err := routing.ExecuteCommand(cmd)
	if err != nil {
		log.Debugf(Category, "[Engine][Windows] Failed to get DNS: %v", err)
		return nil, true
	}

	lines := strings.Split(out, "\n")

	var dns []string
	isDHCP := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.Contains(line, "DHCP") {
			isDHCP = true
		}

		if ip := net.ParseIP(line); ip != nil {
			dns = append(dns, ip.String())
		}
	}

	log.Debugf(Category, "[Engine][Windows] Current DNS: DHCP=%v DNS=%v", isDHCP, dns)

	return dns, isDHCP
}
