package healthcheck

import (
	"go_module/healthcheck/common"
	"go_module/healthcheck/interfacecheck"
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
	stopHealthCheckChannel chan struct{}
	healthCheckStarted     bool = false
	healthCheckStartedMu   sync.Mutex
)

// Default variables
var (
	dnsTimeout             = 1 * time.Second
	pingTimeout            = 3 * time.Second
	delayTimeoutConnecting = 3 * time.Second
	delayTimeoutConnected  = 10 * time.Second
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
			return interfacecheck.VpnInterfacesCheck([]string{"tun", "tap", "ppp", "ipsec", "wg", "awg", "awg0", "tun0", "dobby233", "utun0"})
		},
		func() error {
			return dnsResolveCheck("google.com")
		},
		func() error {
			return dnsResolveCheck("one.one.one.one")
		},
		func() error {
			return pingHostCheck("https://www.google.com/generate_204")
		},
		func() error {
			return pingHostCheck("https://www.cloudflare.com/cdn-cgi/trace")
		},
		func() error {
			return pingHostCheck("https://about.google")
		},
	}
)

func GetConnectionState() ConnectionState {
	log.Debugf(common.Category, "Called GetConnectionState")

	csMu.Lock()
	defer csMu.Unlock()
	return connectionState
}

func InitHealthCheck() {
	log.Debugf(common.Category, "Called InitHealthCheck")
}

func StartHealthCheck() {
	log.Debugf(common.Category, "Called StartHealthCheck")
	healthCheckStartedMu.Lock()
	defer healthCheckStartedMu.Unlock()

	if healthCheckStarted {
		log.Debugf(common.Category, "Health check already running")
	} else {
		log.Debugf(common.Category, "Starting health check")
		healthCheckStarted = true
		stopHealthCheckChannel = make(chan struct{}, 1)
		go innerHealthCheck(stopHealthCheckChannel)
	}
}

func StopHealthCheck() {
	log.Debugf(common.Category, "Called StopHealthCheck")

	healthCheckStartedMu.Lock()
	if healthCheckStarted {
		select {
		case stopHealthCheckChannel <- struct{}{}:
			log.Debugf(common.Category, "Health check stop requested")
		default:
			log.Debugf(common.Category, "Health check stop already requested")
		}
	}
	healthCheckStartedMu.Unlock()
}

func innerHealthCheck(stopCh <-chan struct{}) {
	log.Debugf(common.Category, "Health check started")

	switchState(Connecting)
	for {
		var delayTimeout time.Duration

		csMu.Lock()
		if connectionState == Connecting {
			delayTimeout = delayTimeoutConnecting
		} else {
			delayTimeout = delayTimeoutConnected
		}
		csMu.Unlock()

		select {
		case <-stopCh:
			log.Debugf(common.Category, "Health check stopped")
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
		log.Debug(
			common.Category,
			"Switching connection state",
			map[string]any{"state": newState},
		)
		connectionState = newState
	}
}

func healthCheckStep() {
	log.Debugf(common.Category, "Health check step")

	for _, check := range connectionChecks {
		if err := check(); err != nil {
			log.Error(
				common.Category,
				"Failed check",
				map[string]any{"error": err.Error()},
			)
			switchState(Connecting)
			return
		}
	}

	log.Infof(common.Category, "Health check succeed")
	switchState(Connected)
}
