//go:build android

package dobbyvpn

import (
	"go_module/log"
)

func InitLogger(path string) bool {
	return log.SetPath(path) == nil
}
