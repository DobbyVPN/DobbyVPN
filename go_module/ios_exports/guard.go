//go:build ios

package cloak_outline

import (
	"fmt"
	"go_module/log"
	"runtime/debug"
)

func init() {
	// iOS Network Extensions are typically killed above ~50 MB of physical memory.
	// A 35 MB soft limit tells the Go GC to run aggressively before we hit that ceiling.
	// Combined with a lower GOGC, this reduces peak heap at the cost of slightly more
	// frequent (but shorter) GC pauses - an acceptable trade-off inside a VPN extension.
	debug.SetMemoryLimit(35 * 1024 * 1024)
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

func guardErr(fn string, errp *error) func() {
	return func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("[ios_exports] panic in %s: %v", fn, r)
			log.Infof("%s\n%s", msg, string(debug.Stack()))
			if errp != nil {
				*errp = fmt.Errorf("%s", msg)
			}
		}
	}
}

func guardStatus(fn string, statusp *int32) func() {
	return func() {
		if r := recover(); r != nil {
			log.Infof("[ios_exports] panic in %s: %v\n%s", fn, r, string(debug.Stack()))
			if statusp != nil {
				*statusp = -1
			}
		}
	}
}
