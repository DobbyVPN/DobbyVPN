//go:build !(android || ios)

package interfacecheck

import (
	"errors"
	"fmt"
	"go_module/log"
	"net"
	"slices"
)

var ErrVpnInterfaceCheck = errors.New("vpn interface check error")

func VpnInterfacesCheck(expectedIfaces []string) error {
	log.Infof("[HC] Check: vpn interfaces")

	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed fetch local interfaces: %w", err)
	}

	foundIface := ""
	for _, iface := range ifaces {
		log.Infof("[HC] Checking VPN interface %s", iface.Name)
		if slices.Contains(expectedIfaces, iface.Name) {
			log.Infof("[HC] Found VPN interface %s", iface.Name)
			foundIface = iface.Name
			break
		}
	}

	if foundIface != "" {
		return nil
	}
	log.Infof("[HC] There is no expected net interface")
	return ErrVpnInterfaceCheck
}
