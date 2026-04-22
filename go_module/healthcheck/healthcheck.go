package healthcheck

import (
	"errors"
	"fmt"
	"go_module/common"
	"go_module/log"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

type ConnectionState uint8

const (
	Disconnected ConnectionState = 0
	Connecting   ConnectionState = 1
	Connected    ConnectionState = 2
)

// Healthcheck management
var (
	stopHealthCheckChannel = make(chan bool, 1)
	healthCheckStarted     = false
	healthCheckStartedMu   sync.Mutex
)

// Default variables
var (
	expectedIfaces = []string{"tun", "tap", "ppp", "ipsec", "wg", "awg", "tun0", "outline233"}
	pingTimeout    = 1 * time.Second
	delayTimeout   = 1 * time.Second
	pingCheckHosts = []string{"https://google.com/gen_204", "https://1.1.1.1"}
)

// Connection state
var (
	connectionState ConnectionState = Disconnected
	csMu            sync.Mutex
)

// Check errors
var (
	ConnectionCheckError   = errors.New("connection check error")
	ClientHealthCheckError = errors.New("client health check error")
	VpnInterfaceCheckError = errors.New("vpn interface check error")
)

func GetConnectionState() ConnectionState {
	log.Infof("[HC] Called GetConnectionState")

	csMu.Lock()
	defer csMu.Unlock()
	return connectionState
}

func StartHealthCheck() {
	log.Infof("[HC] Called StartHealthCheck")
	healthCheckStartedMu.Lock()
	defer healthCheckStartedMu.Unlock()

	if healthCheckStarted {
		log.Infof("[HC] Health check already running")
	} else {
		log.Infof("[HC] Starting healtch check")
		go innerHealthCheck()
	}
}

func StopHealthCheck() {
	log.Infof("[HC] Called StopHealthCheck")

	stopHealthCheckChannel <- true
}

func innerHealthCheck() {
	for {
		select {
		case <-stopHealthCheckChannel:
			log.Infof("[HC] Health check stopped")
			return
		case <-time.After(delayTimeout):
			healthCheckStep()
		}
	}
}

func switchState(newState ConnectionState) {
	csMu.Lock()
	defer csMu.Unlock()

	if connectionState != newState {
		log.Infof("[HC] Switching connection state to %v", newState)
		connectionState = newState
	}
}

func healthCheckStep() {
	log.Infof("[HC] Health check step")

	var err error

	err = connectionCheck()
	if err != nil {
		log.Infof("[HC] Failed connection check")
		switchState(Disconnected)
		return
	}

	err = activeClientsCheck()
	if err != nil {
		log.Infof("[HC] Failed active clients check")
		switchState(Connecting)
		return
	}

	err = vpnInterfacesCheck()
	if err != nil {
		log.Infof("[HC] Failed vpn interfaces check: %v", err)
		switchState(Connecting)
		return
	}

	err = pingHostsCheck()
	if err != nil {
		log.Infof("[HC] Failed ping hosts check: %v", err)
		switchState(Connecting)
		return
	}

	log.Infof("[HC] Health check succeed")
	switchState(Connected)
	return
}

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

func vpnInterfacesCheck() error {
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

func pingHostsCheck() error {
	log.Infof("[HC] Check: ping default hosts")

	for _, host := range pingCheckHosts {
		err := pingHostCheck(host)
		if err != nil {
			return fmt.Errorf("Failed host %s: %v", host, err)
		}
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
