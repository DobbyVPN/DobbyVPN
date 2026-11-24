// Package exportguard contains the panic boundary used by native exports.
package exportguard

import (
	"runtime/debug"

	"core/log"
)

// Guard catches a panic at an export boundary without mutating caller state.
func Guard(category, fnName string) func() {
	return func() {
		if recovered := recover(); recovered != nil && category != "" {
			log.Errorf(category, "panic in %s: %v\n%s", fnName, recovered, debug.Stack())
		}
	}
}
