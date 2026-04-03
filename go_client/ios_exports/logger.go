package cloak_outline

import (
	"go_client/log"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	if err := log.SetPath(path); err != nil {
		log.Infof("[ios_exports] InitLogger failed: %v", err)
	}
}
