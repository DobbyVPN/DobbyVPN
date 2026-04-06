package api

import (
	"go_module/log"
)

func InitLogger(path string) {
	log.SetPath(path)
}
