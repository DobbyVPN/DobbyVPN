package healthcheck

import (
	"context"
	"errors"
	"fmt"
	"go_module/common"
	"go_module/log"
	"net"
	"net/http"
	"slices"
)

// Check errors
var (
	ConnectionCheckError   = errors.New("connection check error")
	ClientHealthCheckError = errors.New("client health check error")
	VpnInterfaceCheckError = errors.New("vpn interface check error")
)

func connectionCheck() error {
	log.Infof("[HC] Check: connection check")
	activeClients := common.Client.GetClientNames(true)

	if len(activeClients) == 0 {
		log.Infof("[HC] No vpn clients turned on")

		return ConnectionCheckError
	}

	return nil
}

func activeClientsCheck() error {
	log.Infof("[HC] Check: clients health checks")
	activeClients := common.Client.GetClientNames(true)

	for _, clientName := range activeClients {
		err := common.Client.HealthCheck(clientName)
		if err != nil {
			return ClientHealthCheckError
		}
	}

	return nil
}

func vpnInterfacesCheck(expectedIfaces []string) error {
	log.Infof("[HC] Check: vpn interfaces")

	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("Failed fetch local interfaces: %v", err)
	}

	var foundIface string = ""
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
	} else {
		log.Infof("[HC] There is no expected net interface")

		csMu.Lock()
		defer csMu.Unlock()

		if connectionState != Connecting {
			log.Infof("[HC] connectionState => Connecting")
			connectionState = Connecting
		}

		return VpnInterfaceCheckError
	}
}

func dnsResolveCheck(host string) error {
	log.Infof("[HC] Check: dns resolution check %s", host)

	log.Infof("[HC] With timeout = %v", dnsTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), dnsTimeout)
	defer cancel()

	_, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return err
	}

	return nil
}

func pingHostCheck(host string) error {
	log.Infof("[HC] Check: ping hosts %s", host)

	log.Infof("[HC] With timeout = %v", pingTimeout)
	client := &http.Client{
		Timeout: pingTimeout,
	}

	log.Infof("[HC] Sending GET request to %s", host)
	resp, err := client.Get(host)
	if err != nil {
		return fmt.Errorf("Failed request: %v", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("Invalid status code: %d", resp.StatusCode)
	}

	return nil
}
