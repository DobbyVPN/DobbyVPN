package main

import "C"
import (
	"go_module/log"
)

//export InitLogger
func InitLogger(path *C.char) {
	log.SetPath(C.GoString(path))
}

//export InitTelemetry
func InitTelemetry(endpoint *C.char) {
	log.SetTelemetry(C.GoString(endpoint))
}
