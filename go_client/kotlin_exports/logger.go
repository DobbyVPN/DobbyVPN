package main

import "C"
import (
	"go_client/log"
)

//export InitLogger
func InitLogger(path *C.char) {
	log.SetPath(C.GoString(path))
}
