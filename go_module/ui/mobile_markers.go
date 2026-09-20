//go:build android || ios

package ui

// MarkStartup records the native app initialization milestone where the
// platform bridge supports it. It is intentionally a diagnostic marker, not
// a substitute for rendered-control tests.
func MarkStartup() { markNativeStartup() }

func markUIAttached() { markNativeUIAttached() }

// Mobile shells own the app-group/private-filesystem log locations and may
// inject a DiagnosticStore when their native adapter is ready. There is no
// safe desktop path that the shared mobile process can guess.
func newDefaultDiagnosticStore() DiagnosticStore { return nil }
