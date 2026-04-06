package api

import (
	"go_client/log"
)

func InitLogger(path string) {
	log.SetPath(path)
}
