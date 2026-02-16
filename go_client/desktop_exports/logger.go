package main

import (
	log "go_client/logger"
)

func InitLogger(path string) {
	log.SetPath(path)
}
