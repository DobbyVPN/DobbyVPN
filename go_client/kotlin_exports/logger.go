package main

import "C"
import (
	log "go_client/logger"
)

//export StartCloakClient
func InitLogger(path string) {
	log.SetPath(path)
}
