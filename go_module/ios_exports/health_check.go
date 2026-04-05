package cloak_outline

import (
	"go_module/healthcheck"
	"go_module/log"
)

func TcpPing(address string) (int32, error) {
	defer guard("TcpPing")()
	return healthcheck.TCPPing(address)
}

func UrlTest(url string, standard int) (int32, error) {
	defer guard("UrlTest")()
	return healthcheck.URLTest(url, standard)
}

func CheckServerAlive(address string, port int) int32 {
	defer guard("CheckServerAlive")()
	res := healthcheck.CheckServerAlive(address, port)
	log.Infof("Health check result: %v", res)
	if res == nil {
		return 0
	}
	return -1
}
