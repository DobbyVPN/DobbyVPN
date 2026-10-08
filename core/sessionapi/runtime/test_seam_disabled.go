//go:build !dobbyvpn_test_seams

package runtime

import "time"

// configureTestSeams is deliberately empty in ordinary product builds. The
// hardening fault injector is compiled only into an explicitly requested
// build-local qualification binary.
func configureTestSeams(*Options) {}

// This inert hook keeps test-only monitor timing out of ordinary product
// binaries.
func testHealthMonitorTiming(interval time.Duration, threshold int) (time.Duration, int) {
	return interval, threshold
}
