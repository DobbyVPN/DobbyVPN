//go:build ios

package dobbyvpn

import (
	"go_module/log"
)

func InitLogger(path string) (ready bool) {
	defer guard("InitLogger")()
	if err := log.SetPath(path); err != nil {
		log.Debugf("ios_exports", "InitLogger failed")
		return false
	}
	return true
}
