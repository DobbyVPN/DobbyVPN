//go:build !(android || ios)

package api

import (
	"go_module/log"
)

func InitLogger(path string) error {
	return log.SetPath(path)
}
