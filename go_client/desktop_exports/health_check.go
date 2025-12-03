package main

import "C"
import (
	log "github.com/sirupsen/logrus"
	"go_client/common"
	"go_client/healthcheck"
)

//export StartHealthCheck
func StartHealthCheck(period int, sendMetrics bool) {
	healthcheck.StartHealthCheck(period, sendMetrics)
}

//export StopHealthCheck
func StopHealthCheck() {
	healthcheck.StopHealthCheck()
}

//export Status
func Status() string {
	return healthcheck.Status()
}

//export TcpPing
func TcpPing(address string) (int32, error) {
	return healthcheck.TcpPing(address)
}

//export UrlTest
func UrlTest(url string, standard int) (int32, error) {
	return healthcheck.UrlTest(url, standard)
}

//export CouldStart
func CouldStart() bool {
	log.Infof("Call CouldStart: %v\n", common.Client.CouldStart())
	return common.Client.CouldStart()
}
