//go:build android

package dobbyvpn

import (
	"sync"

	"core/diagnostics"
	"core/log"
)

var (
	loggerInitMu sync.Mutex
	loggerReady  bool
)

func InitLogger(path string) bool {
	return initializeLogger(path) == ""
}

func initializeLogger(path string) string {
	loggerInitMu.Lock()
	defer loggerInitMu.Unlock()
	if loggerReady {
		return ""
	}
	if err := diagnostics.CaptureStderr(path+".stderr", path); err != nil {
		return err.Error()
	}
	if err := log.SetPath(path); err != nil {
		return err.Error()
	}
	loggerReady = true
	log.Infof("android_exports", "Go app logger initialized")
	return ""
}
