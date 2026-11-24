package sessionapi

import (
	"context"
	"time"
)

// healthRecoveryDelays is the pause before each health-recovery restart since
// the attempt last proved healthy: 5, 15, 30, 60 s, then every 2 min. It
// mirrors the schedule of the automatic retry from FAILED (#508) without
// depending on it.
var healthRecoveryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute}

func healthRecoveryDelay(restart int) time.Duration {
	return healthRecoveryDelays[min(restart, len(healthRecoveryDelays)-1)]
}

// sleepStartDelay waits d unless ctx ends first and reports whether the wait
// completed. It is the clock of the restart pause; tests replace it.
var sleepStartDelay = func(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
