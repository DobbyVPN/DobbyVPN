//go:build android

package main

import "C"
import (
	"go_module/log"
	"strings"
)

//export InitLogger
func InitLogger(path string) {
	log.SetPath(strings.Clone(path))
}

//export InitTelemetry
func InitTelemetry(endpoint *C.char) {
	log.SetTelemetry(C.GoString(endpoint))
}
