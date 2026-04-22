//go:build android

package main

import "C"
import (
	"go_module/log"
)

//export InitLogger
func InitLogger(path string) {
	log.SetPath(path)
}
