//go:build android
// +build android

package exported_client

import "github.com/sirupsen/logrus"

// On Android we must never terminate the whole app process from the Go layer.
// Cloak uses logrus.Fatal() in some goroutines (e.g., Accept loop). By default
// that calls os.Exit(1) and kills the entire Android process without a Java crash.
//
// We suppress ExitFunc globally on Android and handle lifecycle in Kotlin/Service.
func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}
}


