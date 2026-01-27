package cloak_outline

import (
	log "go_client/logger"
	"runtime/debug"
)

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