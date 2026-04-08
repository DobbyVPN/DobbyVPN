//go:build android

package main

import "C"
import (
	"go_module/log"
)

//export InitLogger
func InitLogger(path *C.char) {
	log.SetPath(C.GoString(path))
}
