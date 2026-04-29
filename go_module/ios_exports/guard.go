package cloak_outline

import (
	"go_module/log"
	"runtime/debug"
)

func init() {
	// iOS Network Extensions are typically killed above ~50 MB of physical memory.
	// A 45 MB soft limit tells the Go GC to run aggressively before we hit that ceiling.
	// Combined with a lower GOGC, this reduces peak heap at the cost of slightly more
	// frequent (but shorter) GC pauses — an acceptable trade-off inside a VPN extension.
	debug.SetMemoryLimit(30 * 1024 * 1024)
	debug.SetGCPercent(50)
}

// guard logs panics at the gomobile boundary so the iOS tunnel doesn't "silently die".
// Note: panics in Go called from Swift/NEPacketTunnelProvider can terminate the extension
// without a Swift stack trace.
func guard(fn string) func() {
	return func() {
		if r := recover(); r != nil {
			log.Infof("[ios_exports] panic in %s: %v\n%s", fn, r, string(debug.Stack()))
		}
	}
}
