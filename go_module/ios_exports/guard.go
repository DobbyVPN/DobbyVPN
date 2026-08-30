//go:build ios

package dobbyvpn

import (
	"runtime/debug"

	"go_module/exportguard"
)

func init() {
	// iOS Network Extensions are typically killed above ~50 MB of physical memory.
	// A 35 MB soft limit tells the Go GC to run aggressively before we hit that ceiling.
	// Combined with a lower GOGC, this reduces peak heap at the cost of slightly more
	// frequent (but shorter) GC pauses - an acceptable trade-off inside a VPN extension.
	debug.SetMemoryLimit(35 * 1024 * 1024)
	debug.SetGCPercent(50)
}

func guard(fn string) func() {
	return exportguard.Guard(logCategory, fn)
}

func guardErr(fn string, errp *error) func() {
	return exportguard.GuardErr(logCategory, fn, errp)
}

func guardStatus(fn string, statusp *int32) func() {
	return exportguard.GuardStatus(logCategory, fn, statusp)
}
