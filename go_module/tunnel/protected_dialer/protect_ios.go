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
		log.Debugf(Category, "Default interface index changed: %d -> %d interfaces=[%s]", oldIndex, index, describeInterfacesForLog())
	} else {
		log.Debugf(Category, "Default interface index unchanged: %d", index)
	}
}

// GetDefaultInterfaceForIOS returns the current default interface index.
func GetDefaultInterfaceForIOS() int {
	defaultInterfaceMu.RLock()
	idx := defaultInterfaceIndex
	defaultInterfaceMu.RUnlock()
	if idx > 0 {
		return idx
	}

	log.Debugf(Category, "default interface index not set; scanning interfaces")
	idx = detectDefaultInterfaceIndex()
	if idx > 0 {
		SetDefaultInterfaceForIOS(idx)
	}
	return idx
}

// GetConfiguredDefaultInterfaceForIOS returns the last interface index supplied by Swift.
// Unlike GetDefaultInterfaceForIOS, it does not perform fallback detection; this is useful
// for diagnostics that must distinguish "Swift never supplied an index" from "Go guessed one".
func GetConfiguredDefaultInterfaceForIOS() int {
	defaultInterfaceMu.RLock()
	idx := defaultInterfaceIndex
	defaultInterfaceMu.RUnlock()
	return idx
}

func ProtectionDiagnosticsForIOS() string {
	configuredIndex := GetConfiguredDefaultInterfaceForIOS()
	return fmt.Sprintf(
		"configuredDefaultInterfaceIndex=%d configuredDefaultInterfaceName=%s interfaces=[%s]",
		configuredIndex,
		interfaceNameByIndex(configuredIndex),
		describeInterfacesForLog(),
	)
}

func detectDefaultInterfaceIndex() int {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Debugf(Category, "interface scan failed: %v", err)
		return 0
	}
	log.Debugf(Category, "interface scan candidates=[%s]", formatInterfacesForLog(interfaces))

	preferredNames := []string{"en0", "pdp_ip0"}
	for _, name := range preferredNames {
		for _, iface := range interfaces {
			if iface.Name == name && iface.Flags&net.FlagUp != 0 {
				log.Debugf(Category, "detected default interface fallback: %s index=%d", iface.Name, iface.Index)
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
		log.Debugf(Category, "detected default interface fallback: %s index=%d", iface.Name, iface.Index)
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

func interfaceNameByIndex(index int) string {
	if index <= 0 {
		return "unset"
	}
	iface, err := net.InterfaceByIndex(index)
	if err != nil {
		return fmt.Sprintf("lookup_error=%v", err)
	}
	return iface.Name
}

type iosProtector struct{}

func (i *iosProtector) Diagnostics() string {
	return ProtectionDiagnosticsForIOS()
}

func (i *iosProtector) Protect(fd uintptr, network string) error {
	// iOS 26+: Try SO_NO_TC_NETPOLICY first (legacy approach for older iOS versions).
	// On iOS 26+, this fails with "invalid argument" — expected and handled below.
	legacyErr := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, SO_NO_TC_NETPOLICY, 1)
	if legacyErr != nil {
		log.Debugf(Category, "SO_NO_TC_NETPOLICY failed fd=%d network=%s err=%v", fd, network, legacyErr)
	} else {
		log.Debugf(Category, "SO_NO_TC_NETPOLICY success fd=%d network=%s", fd, network)
	}

	// iOS 26+: Use IP_BOUND_IF with the actual interface index.
	// This is the primary method for socket protection on iOS 26+.
	// The interface index is provided by Swift via NWPathMonitor.
	ifaceIndex := GetDefaultInterfaceForIOS()

	if ifaceIndex > 0 {
		log.Debugf(Category, "Protect binding fd=%d network=%s ifaceIndex=%d ifaceName=%s", fd, network, ifaceIndex, interfaceNameByIndex(ifaceIndex))
		// Bind the socket to the default physical interface (WiFi/Cellular)
		// This ensures encrypted VPN traffic goes outside the VPN tunnel.
		switch network {
		case "tcp4", "udp4":
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex); err != nil {
				log.Debugf(Category, "IP_BOUND_IF (IPv4) failed for fd=%d iface=%d: %v", fd, ifaceIndex, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Debugf(Category, "IP_BOUND_IF (IPv4) success: fd=%d bound to interface %d", fd, ifaceIndex)
			if err := verifyBoundInterface(fd, syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex, "IP_BOUND_IF", network); err != nil {
				return err
			}
		case "tcp6", "udp6":
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IPV6, IPV6_BOUND_IF, ifaceIndex); err != nil {
				log.Debugf(Category, "IP_BOUND_IF (IPv6) failed for fd=%d iface=%d: %v", fd, ifaceIndex, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Debugf(Category, "IP_BOUND_IF (IPv6) success: fd=%d bound to interface %d", fd, ifaceIndex)
			if err := verifyBoundInterface(fd, syscall.IPPROTO_IPV6, IPV6_BOUND_IF, ifaceIndex, "IPV6_BOUND_IF", network); err != nil {
				return err
			}
		default:
			// For unknown network types, try IPv4
			if err := syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex); err != nil {
				log.Debugf(Category, "IP_BOUND_IF (fallback) failed for fd=%d iface=%d network=%s: %v", fd, ifaceIndex, network, err)
				if legacyErr != nil {
					return err
				}
				return nil
			}
			log.Debugf(Category, "IP_BOUND_IF (fallback IPv4) success: fd=%d bound to interface %d network=%s", fd, ifaceIndex, network)
			if err := verifyBoundInterface(fd, syscall.IPPROTO_IP, IP_BOUND_IF, ifaceIndex, "IP_BOUND_IF fallback", network); err != nil {
				return err
			}
		}
	} else {
		// No interface index set - log with full interface list to aid debugging
		log.Debugf(Category, "WARNING: No default interface index set (ifaceIndex=%d) interfaces=[%s]. "+
			"VPN traffic may not bypass tunnel correctly on iOS 26+.", ifaceIndex, describeInterfacesForLog())
		if legacyErr != nil {
			return fmt.Errorf("no default interface index set and SO_NO_TC_NETPOLICY failed: %w", legacyErr)
		}
	}

	return nil
}

func verifyBoundInterface(fd uintptr, level int, option int, expected int, label string, network string) error {
	actual, err := syscall.GetsockoptInt(int(fd), level, option)
	if err != nil {
		log.Debugf(Category, "%s verify skipped fd=%d network=%s expectedIface=%d getErr=%v", label, fd, network, expected, err)
		return nil
	}
	if actual != expected {
		err := fmt.Errorf("%s verification mismatch fd=%d network=%s expectedIface=%d actualIface=%d", label, fd, network, expected, actual)
		log.Debugf(Category, "%v", err)
		return err
	}
	log.Debugf(Category, "%s verify OK fd=%d network=%s iface=%d", label, fd, network, actual)
	return nil
}

func init() {
	protector = &iosProtector{}
	log.Debugf(Category, "Initialized with iOS 26+ support (SO_NO_TC_NETPOLICY + IP_BOUND_IF with dynamic interface)")
}
