package healthcheck

import (
	"go_module/log"
	"sync"
	"time"
)

type ConnectionState uint8
type ConnectionCheck func() error

const (
	Disconnected ConnectionState = 0
	Connecting   ConnectionState = 1
	Connected    ConnectionState = 2
)

// Healthcheck management
var (
	stopHealthCheckChannel chan bool
	healthCheckStarted     bool = false
	healthCheckStartedMu   sync.Mutex
)

// Default variables
var (
	dnsTimeout   = 1 * time.Second
	pingTimeout  = 1 * time.Second
	delayTimeout = 1 * time.Second
)

// Connection state
var (
	connectionState ConnectionState = Disconnected
	csMu            sync.Mutex
)

// Connection checks
var (
	connectionChecks []ConnectionCheck = []ConnectionCheck{
		connectionCheck,
		activeClientsCheck,
		func() error {
			return vpnInterfacesCheck([]string{"tun", "tap", "ppp", "ipsec", "wg", "awg", "awg0", "tun0", "outline233"})
		},
		func() error {
			return dnsResolveCheck("google.com")
		},
		func() error {
			return dnsResolveCheck("one.one.one.one")
		},
		func() error {
			return pingHostCheck("https://google.com/gen_204")
		},
		func() error {
			return pingHostCheck("https://1.1.1.1")
		},
	}
)

func GetConnectionState() ConnectionState {
	log.Infof("[HC] Called GetConnectionState")

	csMu.Lock()
	defer csMu.Unlock()
	return connectionState
}

func InitHealthCheck() {
	log.Infof("[HC] Called InitHealthCheck")
	// Telemetry initiation etc...
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

	healthCheckStartedMu.Lock()
	if healthCheckStarted {
		stopHealthCheckChannel <- true
	}
	healthCheckStartedMu.Unlock()
}

func innerHealthCheck() {
	log.Infof("[HC] Health check started")
	healthCheckStartedMu.Lock()
	healthCheckStarted = true
	stopHealthCheckChannel = make(chan bool, 1)
	healthCheckStartedMu.Unlock()

	switchState(Connecting)
	for {
		select {
		case <-stopHealthCheckChannel:
			log.Infof("[HC] Health check stopped")
			switchState(Disconnected)
			healthCheckStartedMu.Lock()
			healthCheckStarted = false
			healthCheckStartedMu.Unlock()
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

	for _, check := range connectionChecks {
		if err := check(); err != nil {
			log.Infof("[HC] Failed check: %v", err)
			switchState(Connecting)
			return
		}
	}

	log.Infof("[HC] Health check succeed")
	switchState(Connected)
}
