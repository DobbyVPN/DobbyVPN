package main

import (
<<<<<<< HEAD
<<<<<<< HEAD
<<<<<<< HEAD
	"C"
=======
=======
    "C"
>>>>>>> 1da330d (Fix fast con/dis on mobile and fix CI)
	"go_client/common"
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
	"C"
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

<<<<<<< HEAD
<<<<<<< HEAD
//export CouldStart
func CouldStart() bool {
	return true
<<<<<<< HEAD
=======
=======
//export CouldStart
>>>>>>> 1da330d (Fix fast con/dis on mobile and fix CI)
func CouldStart() bool {
	return common.Client.CouldStart()
>>>>>>> c3c2f56 (Fix fast connect/disconnect on windows)
=======
>>>>>>> 7039ac7 (Rollback status marking)
}
