//go:build android || ios

package runtime

// Mobile platforms have no interrupted-state recovery.
func recoverBeforeNetwork() error { return nil }
