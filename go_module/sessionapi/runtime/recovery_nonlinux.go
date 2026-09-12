//go:build !linux

package runtime

// RecoverInterruptedState is a no-op on platforms whose product-owned
// interrupted-state recovery is not Linux policy routing. The desktop shell
// still calls this common product hook on every desktop platform.
func RecoverInterruptedState() error { return nil }
