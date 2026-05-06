package cloak_outline

import (
	"go_module/log"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	log.Infof("[ios_exports] InitLogger begin path=%s", path)
	if err := log.SetPath(path); err != nil {
		log.Errorf(Category, "InitLogger failed: %v", err)
		return
	}
	log.Infof("[ios_exports] InitLogger OK path=%s", path)
	logNativeBuildInfo("[ios_exports] InitLogger")
}
