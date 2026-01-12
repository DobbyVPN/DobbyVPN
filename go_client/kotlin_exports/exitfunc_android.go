//go:build android
// +build android

package main

import "github.com/sirupsen/logrus"

// Prevent Go/logrus Fatal from terminating the whole Android app process.
// Some dependencies use logrus.Fatal in goroutines; by default it calls os.Exit(1),
// which appears as "Process ... exited cleanly (1)" without a Java FATAL EXCEPTION.
func init() {
	logrus.StandardLogger().ExitFunc = func(int) {}
}
