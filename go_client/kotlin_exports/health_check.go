package kotlin_exports

import "go_client/healthcheck"

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
