//go:build ios

package cloak_outline

import (
	"go_module/log"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	log.Debugf(Category, "InitLogger begin path=%s", path)
	if err := log.SetPath(path); err != nil {
		log.Errorf(Category, "InitLogger failed: %v", err)
		return
	}
	log.Debugf(Category, "InitLogger OK path=%s", path)
	logNativeBuildInfo("InitLogger")
}
