package main

import (
	"go_client/common"
	"go_client/healthcheck"

	log "github.com/sirupsen/logrus"
)

func StartHealthCheck(period int, sendMetrics bool) {
	healthcheck.StartHealthCheck(period, sendMetrics)
}

func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}

func Status() string {
	return healthcheck.Status()
}

func TcpPing(address string) (int32, error) {
	return healthcheck.TcpPing(address)
}

func UrlTest(url string, standard int) (int32, error) {
	return healthcheck.UrlTest(url, standard)
}

func CouldStart() bool {
	log.Infof("Call CouldStart: %v\n", common.Client.CouldStart())
	return common.Client.CouldStart()
}
