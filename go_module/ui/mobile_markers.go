//go:build android || ios

package ui

// MarkStartup records the native app initialization milestone where the
// platform bridge supports it. It is intentionally a diagnostic marker, not
// a substitute for rendered-control tests.
func MarkStartup() { markNativeStartup() }

func markUIAttached() { markNativeUIAttached() }
