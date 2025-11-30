package main

import "C"
import (
	log "go_client/logger"
)

//export InitLogger
func InitLogger(path *C.char) {
	log.SetPath(C.GoString(path))
}
