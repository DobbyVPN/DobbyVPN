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
	healthCheckGeneration  uint64
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
			return quorumHTTPPingCheck(httpProbeURLs)
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

	if healthCheckStarted {
		log.Debugf(common.Category, "Health check already running; reset counters and request immediate check")
		resetFailedChecks()
		switchState(Connecting)
		wakeCh := wakeHealthCheckChannel
		healthCheckStartedMu.Unlock()
		select {
		case wakeCh <- struct{}{}:
			log.Debugf(common.Category, "Health check wakeup requested")
		default:
			log.Debugf(common.Category, "Health check wakeup already pending")
		}
	} else {
		log.Debugf(common.Category, "Starting health check")
		healthCheckStarted = true
		stopHealthCheckChannel = make(chan struct{}, 1)
		wakeHealthCheckChannel = make(chan struct{}, 1)
		healthCheckGeneration++
		generation := healthCheckGeneration
		stopCh := stopHealthCheckChannel
		wakeCh := wakeHealthCheckChannel
		healthCheckStartedMu.Unlock()
		go innerHealthCheck(stopCh, wakeCh, generation)
	}
}

func StopHealthCheck() {
	log.Debugf(common.Category, "Called StopHealthCheck")

	healthCheckStartedMu.Lock()
	if healthCheckStarted {
		stopCh := stopHealthCheckChannel
		healthCheckStarted = false
		healthCheckGeneration++
		stopHealthCheckChannel = nil
		wakeHealthCheckChannel = nil
		healthCheckStartedMu.Unlock()

		select {
		case stopCh <- struct{}{}:
			log.Debugf(common.Category, "Health check stop requested")
		default:
			log.Debugf(common.Category, "Health check stop already requested")
		}
		switchState(Disconnected)
		resetFailedChecks()
		return
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

func innerHealthCheck(stopCh, wakeCh <-chan struct{}, generation uint64) {
	log.Debugf(common.Category, "Health check started generation=%d", generation)

	switchStateForGeneration(generation, Connecting)
	healthCheckStep(generation)
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
			log.Debugf(common.Category, "Health check stopped generation=%d", generation)
			return
		case <-time.After(delayTimeout):
			healthCheckStep(generation)
		case <-wakeCh:
			log.Debugf(common.Category, "Health check wakeup received generation=%d", generation)
			healthCheckStep(generation)
		}
	}
}

func isHealthCheckGenerationCurrent(generation uint64) bool {
	healthCheckStartedMu.Lock()
	defer healthCheckStartedMu.Unlock()
	return healthCheckStarted && healthCheckGeneration == generation
}

func switchStateForGeneration(generation uint64, newState ConnectionState) {
	if !isHealthCheckGenerationCurrent(generation) {
		log.Debugf(common.Category, "Ignore stale health check state generation=%d state=%d", generation, newState)
		return
	}
	switchState(newState)
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

func healthCheckStep(generation uint64) {
	if !isHealthCheckGenerationCurrent(generation) {
		log.Debugf(common.Category, "Skip stale health check step generation=%d", generation)
		return
	}
	log.Debugf(common.Category, "Health check step generation=%d", generation)

	for _, check := range connectionChecks {
		if !isHealthCheckGenerationCurrent(generation) {
			log.Debugf(common.Category, "Abort stale health check step generation=%d", generation)
			return
		}
		err := check()
		if !isHealthCheckGenerationCurrent(generation) {
			log.Debugf(common.Category, "Ignore stale health check result generation=%d", generation)
			return
		}
		if err != nil {
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
				switchStateForGeneration(generation, Connecting)
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

	if !isHealthCheckGenerationCurrent(generation) {
		log.Debugf(common.Category, "Ignore stale successful health check generation=%d", generation)
		return
	}
	resetFailedChecks()
	log.Infof(common.Category, "Health check succeed")
	switchStateForGeneration(generation, Connected)
}
