//go:build !(android || ios)

package runtime

// recoverBeforeNetwork is a seam for runtime unit tests. It is a no-op outside
// Linux, so Connect behaves as before on every other desktop platform.
var recoverBeforeNetwork = RecoverInterruptedState
