package protected_dialer

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"

	"go_module/log"
)

const SO_NO_TC_NETPOLICY = 0x1101

// IP_BOUND_IF binds the socket to a specific interface by index
// This is the key option for iOS 26+ socket protection
const IP_BOUND_IF = 25
const IPV6_BOUND_IF = 125

// defaultInterfaceIndex stores the current default interface index.
// On iOS, this is updated by Swift code via Network.NWPathMonitor.
var defaultInterfaceIndex int
var defaultInterfaceMu sync.RWMutex

// SetDefaultInterfaceForIOS sets the default interface index from Swift.
// Called when the network path changes (WiFi <-> Cellular transition).
func SetDefaultInterfaceForIOS(index int) {
	defaultInterfaceMu.Lock()
	oldIndex := defaultInterfaceIndex
	defaultInterfaceIndex = index
	defaultInterfaceMu.Unlock()

	if oldIndex != index {
		log.Infof("[iOS-Protect] Default interface index changed: %d -> %d interfaces=[%s]", oldIndex, index, describeInterfacesForLog())
	} else {
		log.Infof("[iOS-Protect] Default interface index unchanged: %d interfaces=[%s]", index, describeInterfacesForLog())
	}
}

// GetDefaultInterfaceForIOS returns the current default interface index.
func GetDefaultInterfaceForIOS() int {
	defaultInterfaceMu.RLock()
	idx := defaultInterfaceIndex
	defaultInterfaceMu.RUnlock()
	if idx > 0 {
		log.Infof("[iOS-Protect] using cached default interface index=%d", idx)
		return idx
	}

	log.Infof("[iOS-Protect] default interface index not set; scanning interfaces")
	idx = detectDefaultInterfaceIndex()
	if idx > 0 {
		SetDefaultInterfaceForIOS(idx)
	}
	return idx
}

func detectDefaultInterfaceIndex() int {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Infof("[iOS-Protect] interface scan failed: %v", err)
		return 0
	}
	log.Infof("[iOS-Protect] interface scan candidates=[%s]", formatInterfacesForLog(interfaces))

	preferredNames := []string{"en0", "pdp_ip0"}
	for _, name := range preferredNames {
		for _, iface := range interfaces {
			if iface.Name == name && iface.Flags&net.FlagUp != 0 {
				log.Infof("[iOS-Protect] detected default interface fallback: %s index=%d", iface.Name, iface.Index)
				return iface.Index
			}
		}
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(iface.Name, "utun") || strings.HasPrefix(iface.Name, "lo") {
			continue
		}
		log.Infof("[iOS-Protect] detected default interface fallback: %s index=%d", iface.Name, iface.Index)
		return iface.Index
	}

	return 0
}

func describeInterfacesForLog() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return fmt.Sprintf("scan_error=%v", err)
	}
	return formatInterfacesForLog(interfaces)
}

func formatInterfacesForLog(interfaces []net.Interface) string {
	parts := make([]string, 0, len(interfaces))
	for _, iface := range interfaces {
		parts = append(parts, fmt.Sprintf("%s(index=%d flags=%s mtu=%d)", iface.Name, iface.Index, iface.Flags.String(), iface.MTU))
	}
	return strings.Join(parts, ";")
}

type iosProtector struct{}

func (i *iosProtector) Protect(fd uintptr, network string) error {
	log.Infof("[iOS-Protect] Protect begin fd=%d network=%s", fd, network)
	// iOS 26+: Try SO_NO_TC_NETPOLICY first (legacy approach for older iOS versions)
	// On iOS 26+, this typically fails with "invalid argument" but doesn't hurt to try.
	legacyErr := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
	if legacyErr != nil {
		log.Infof("[iOS-Protect] SO_NO_TC_NETPOLICY failed (expected on iOS 26+): %v", legacyErr)
	} else {
		log.Infof("[iOS-Protect] SO_NO_TC_NETPOLICY success fd=%d", fd)
	}

	// iOS 26+: Use IP_BOUND_IF with the actual interface index.
	// This is the primary method for socket protection on iOS 26+.
	// The interface index is provided by Swift via NWPathMonitor.
	ifaceIndex := GetDefaultInterfaceForIOS()

	if ifaceIndex > 0 {
		log.Infof("[iOS-Protect] Protect binding fd=%d network=%s ifaceIndex=%d interfaces=[%s]", fd, network, ifaceIndex, describeInterfacesForLog())
		// Bind the socket to the default physical interface (WiFi/Cellular)
		// This ensures encrypted VPN traffic goes outside the VPN tunnel.
		switch network {
		case "tcp4", "udp4":
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex); err != nil {
				log.Infof("[iOS-Protect] IP_BOUND_IF (IPv4) failed for fd=%d iface=%d: %v", fd, ifaceIndex, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Infof("[iOS-Protect] IP_BOUND_IF (IPv4) success: fd=%d bound to interface %d", fd, ifaceIndex)
		case "tcp6", "udp6":
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, IPV6_BOUND_IF, ifaceIndex); err != nil {
				log.Infof("[iOS-Protect] IP_BOUND_IF (IPv6) failed for fd=%d iface=%d: %v", fd, ifaceIndex, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Infof("[iOS-Protect] IP_BOUND_IF (IPv6) success: fd=%d bound to interface %d", fd, ifaceIndex)
		default:
			// For unknown network types, try IPv4
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex); err != nil {
				log.Infof("[iOS-Protect] IP_BOUND_IF (fallback) failed for fd=%d iface=%d network=%s: %v", fd, ifaceIndex, network, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Infof("[iOS-Protect] IP_BOUND_IF (fallback IPv4) success: fd=%d bound to interface %d network=%s", fd, ifaceIndex, network)
		}
	} else {
		// No interface index set - this will likely fail on iOS 26+
		// Log a warning that Swift needs to provide the interface index
		log.Infof("[iOS-Protect] WARNING: No default interface index set (ifaceIndex=%d). "+
			"VPN traffic may not bypass tunnel correctly on iOS 26+. "+
			"Ensure Swift calls SetDefaultInterfaceIndex() with valid interface index from NWPathMonitor.", ifaceIndex)
		if legacyErr != nil {
			return fmt.Errorf("no default interface index set and SO_NO_TC_NETPOLICY failed: %w", legacyErr)
		}
	}

	log.Infof("[iOS-Protect] Protect finished fd=%d network=%s ifaceIndex=%d legacyErr=%v", fd, network, ifaceIndex, legacyErr)
	return nil
}

func init() {
	protector = &iosProtector{}
	log.Infof("[iOS-Protect] Initialized with iOS 26+ support (SO_NO_TC_NETPOLICY + IP_BOUND_IF with dynamic interface)")
}
