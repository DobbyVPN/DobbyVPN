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
	wakeHealthCheckChannel chan struct{}
	healthCheckStarted     bool = false
	healthCheckStartedMu   sync.Mutex
)

// Default variables
var (
	dnsTimeout             = 1 * time.Second
	pingTimeout            = 3 * time.Second
	delayTimeoutConnecting = 3 * time.Second
	delayTimeoutConnected  = 10 * time.Second
	failedCheckThreshold   = 2
)

// Connection state
var (
	connectionState ConnectionState = Disconnected
	csMu            sync.Mutex
	failedChecks    int
	failedChecksMu  sync.Mutex
)

// Connection checks
var (
	connectionChecks []ConnectionCheck = []ConnectionCheck{
		connectionCheck,
		activeClientsCheck,
		func() error {
			return interfacecheck.VpnInterfacesCheck([]string{"tun", "tap", "ppp", "ipsec", "wg", "awg", "awg0", "tun0", "wintun", "dobby233", "utun0"})
		},
		func() error {
			return dnsResolveCheck("google.com")
		},
		func() error {
			return dnsResolveCheck("one.one.one.one")
		},
		func() error {
			return anyHTTPPingCheck([]string{
				"https://www.google.com/generate_204",
				"https://www.cloudflare.com/cdn-cgi/trace",
				"https://about.google",
			})
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
	switchState(Disconnected)
	resetFailedChecks()
}

func StartHealthCheck() {
	log.Debugf(common.Category, "Called StartHealthCheck")
	healthCheckStartedMu.Lock()
	defer healthCheckStartedMu.Unlock()

	if healthCheckStarted {
		log.Debugf(common.Category, "Health check already running; reset counters and request immediate check")
		resetFailedChecks()
		switchState(Connecting)
		select {
		case wakeHealthCheckChannel <- struct{}{}:
			log.Debugf(common.Category, "Health check wakeup requested")
		default:
			log.Debugf(common.Category, "Health check wakeup already pending")
		}
	} else {
		log.Debugf(common.Category, "Starting health check")
		healthCheckStarted = true
		stopHealthCheckChannel = make(chan struct{}, 1)
		wakeHealthCheckChannel = make(chan struct{}, 1)
		go innerHealthCheck(stopHealthCheckChannel, wakeHealthCheckChannel)
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

func resetFailedChecks() {
	failedChecksMu.Lock()
	failedChecks = 0
	failedChecksMu.Unlock()
}

func recordFailedCheck() int {
	failedChecksMu.Lock()
	defer failedChecksMu.Unlock()
	failedChecks++
	return failedChecks
}

func innerHealthCheck(stopCh <-chan struct{}, wakeCh <-chan struct{}) {
	log.Debugf(common.Category, "Health check started")

	switchState(Connecting)
	healthCheckStep()
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
			resetFailedChecks()
			healthCheckStartedMu.Lock()
			healthCheckStarted = false
			healthCheckStartedMu.Unlock()
			return
		case <-time.After(delayTimeout):
			healthCheckStep()
		case <-wakeCh:
			log.Debugf(common.Category, "Health check wakeup received")
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
			consecutiveFails := recordFailedCheck()
			if consecutiveFails >= failedCheckThreshold {
				log.Error(
					common.Category,
					"Health check failed",
					map[string]any{
						"error":            err.Error(),
						"consecutiveFails": consecutiveFails,
						"threshold":        failedCheckThreshold,
					},
				)
				switchState(Connecting)
				return
			}

			log.Warnf(
				common.Category,
				"Health check HTTP cycle failed consecutiveFails=%d threshold=%d error=%v",
				consecutiveFails,
				failedCheckThreshold,
				err,
			)
			return
		}
	}

	resetFailedChecks()
	log.Infof(common.Category, "Health check succeed")
	switchState(Connected)
}
