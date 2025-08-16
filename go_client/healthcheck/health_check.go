package healthcheck

import (
	"context"
	"fmt"
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"sync"
	"sync/atomic"
	"time"
)

type healthChecker struct {
	ctx         context.Context
	cancel      context.CancelFunc
	sendMetrics bool
	period      int32
}

var checker *healthChecker
var mu sync.Mutex
var lastStatus atomic.Pointer[healthCheckStatus]

func newHealthCheck(sendMetrics bool, period int32) *healthChecker {
	ctx, cancel := context.WithCancel(context.Background())
	return &healthChecker{ctx: ctx, cancel: cancel, sendMetrics: sendMetrics, period: period}
}

func (h *healthChecker) stop() {
	h.cancel()
}

func (h *healthChecker) start() {
	ticker := time.NewTicker(time.Duration(h.period) * time.Second)
	for {
		select {
		case <-ticker.C:
			status := checkHealth()
			if !status.isHealthy {
				err := common.Client.RefreshAll()
				status.reconnectError = err
				status.reconnected = err == nil
			}
			lastStatus.Store(&status)
			log.Infof(status.String())

		case <-h.ctx.Done():
			ticker.Stop()
			return
		}
	}
}

func checkHealth() healthCheckStatus {
	ms, err := UrlTest("https://www.gstatic.com/generate_204", 1)
	return healthCheckStatus{at: time.Now(), isHealthy: err == nil, handshakeMs: ms, err: err}
}

type healthCheckStatus struct {
	at          time.Time
	isHealthy   bool
	handshakeMs int32
	err         error

	reconnected    bool
	reconnectError error
}

func (s healthCheckStatus) String() string {
	return fmt.Sprintf(
		"at: %v, isHealthy: %v, handshakeMs: %v, err: %v, reconnected: %v, reconnectError: %v",
		s.at,
		s.isHealthy,
		s.handshakeMs,
		s.err,
		s.reconnected,
		s.reconnectError,
	)
}

func StartHealthCheck(period int, sendMetrics bool) {
	mu.Lock()
	defer mu.Unlock()
	if checker != nil {
		checker.stop()
	}
	checker = newHealthCheck(sendMetrics, int32(period))
	go checker.start()
}

func StopHealthCheck() {
	mu.Lock()
	defer mu.Unlock()
	if checker != nil {
		checker.stop()
		checker = nil
	}
}

func Status() string {
	status := lastStatus.Load()
	if status == nil {
		return ""
	}
	return status.String()
}
