package cloak_outline

import (
	log "go_client/logger"
)

func InitLogger(path string) {
	defer guard("InitLogger")()
	if err := log.SetPath(path); err != nil {
		log.Infof("[ios_exports] InitLogger failed: %v", err)
	}
}
