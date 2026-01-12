package cloak_outline

import "go_client/healthcheck"

func StartHealthCheck(period int, sendMetrics bool) {
	defer guard("StartHealthCheck")()
	healthcheck.StartHealthCheck(period, sendMetrics)
}

func StopHealthCheck() {
	defer guard("StopHealthCheck")()
	healthcheck.StopHealthCheck()
}

func Status() string {
	defer guard("Status")()
	return healthcheck.Status()
}

func TcpPing(address string) (int32, error) {
	defer guard("TcpPing")()
	return healthcheck.TcpPing(address)
}

func UrlTest(url string, standard int) (int32, error) {
	defer guard("UrlTest")()
	return healthcheck.UrlTest(url, standard)
}
