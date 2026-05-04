package cloak_outline

import (
	"go_module/netcheck"
	"strings"
)

func NetCheck(configPath string) error {
	return netcheck.NetCheck(strings.Clone(configPath))
}

func CancelNetCheck() {
	netcheck.CancelNetCheck()
}
