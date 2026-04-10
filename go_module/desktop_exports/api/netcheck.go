package api

import (
	"go_module/log"
	"go_module/netcheck"
)

func NetCheck(configPath string) error {
	log.Infof("NetCheck")

	return netcheck.NetCheck(configPath)
}

func CancelNetCheck() {
	log.Infof("CancelNetCheck")

	netcheck.CancelNetCheck()
}
