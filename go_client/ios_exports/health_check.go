package cloak_outline

import (
<<<<<<< HEAD
<<<<<<< HEAD
=======
	"go_client/common"
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
>>>>>>> 7039ac7 (Rollback status marking)
	"go_client/healthcheck"
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
<<<<<<< HEAD
<<<<<<< HEAD
	return true
=======
	return common.Client.CouldStart()
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	return true
>>>>>>> 7039ac7 (Rollback status marking)
}
