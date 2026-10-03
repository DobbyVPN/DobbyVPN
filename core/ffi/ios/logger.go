//go:build ios

package dobbyvpn

import (
	"core/diagnostics"
	"core/log"
)

func InitLogger(path string) (ready bool) {
	defer guard("InitLogger")()
	if err := diagnostics.CaptureStderr(path+".stderr", path); err != nil {
		log.Errorf("ios_exports", "capture stderr failed: %v", err)
		return false
	}
	if err := log.SetPath(path); err != nil {
		log.Errorf("ios_exports", "InitLogger failed: %v", err)
		return false
	}
	return true
}
