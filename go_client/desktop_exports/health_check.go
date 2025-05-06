package main

import "go_client/healthcheck"

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
