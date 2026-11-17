//go:build !(windows || android || ios)

package executor

import "time"

const (
	wakePollInterval = 10 * time.Second
	wakeSkipMinimum  = 5 * time.Second
)

// startSystemEvents reports wake and network changes to the core for as long as
// the process runs.
func startSystemEvents() {
	emit := newNetworkEvents()
	go watchWake(emit)
	go watchNetworkChanges(emit)
}

// The monotonic clock does not advance while the machine sleeps and the wall
// clock does, so a growing difference between them is a wake-up. This avoids
// a D-Bus or IOKit dependency; a wake is noticed within one poll interval.
func watchWake(emit func(reason string)) {
	last := time.Now()
	for range time.Tick(wakePollInterval) {
		now := time.Now()
		if sleptFor(now.Round(0).Sub(last.Round(0)), now.Sub(last)) {
			emit(eventWake)
		}
		last = now
	}
}

func sleptFor(wallElapsed, monotonicElapsed time.Duration) bool {
	return wallElapsed-monotonicElapsed >= wakeSkipMinimum
}
