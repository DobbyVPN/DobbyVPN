//go:build ios
// +build ios

package cloak_outline

import "github.com/sirupsen/logrus"

// Network extensions must not be terminated from Go dependency goroutines.
// Android already suppresses logrus.Fatal exit behavior; iOS needs the same
// protection because the tunnel extension otherwise exits without a Swift error.
func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}
}
