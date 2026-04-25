package api

import (
	"go_module/log"
	"go_module/netcheck"
)

func NetCheck(configPath string) error {
	log.Debugf(Category, "NetCheck")

	return netcheck.NetCheck(configPath)
}

func CancelNetCheck() {
	log.Debugf(Category, "CancelNetCheck")

	netcheck.CancelNetCheck()
}
