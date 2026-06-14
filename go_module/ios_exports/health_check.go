//go:build ios

package cloak_outline

import (
	"go_module/healthcheck"
	"go_module/log"
)

func TcpPing(address string) (ret int32, err error) {
	defer guardErr("TcpPing", &err)()
	log.Debugf("ios_exports", "TcpPing begin address=%s", address)
	ret, err = healthcheck.TCPPing(address)
	if err != nil {
		log.Debugf("ios_exports", "TcpPing failed address=%s err=%v", address, err)
		return ret, err
	}
	log.Debugf("ios_exports", "TcpPing OK address=%s ms=%d", address, ret)
	return ret, nil
}

func UrlTest(url string, standard int) (ret int32, err error) {
	defer guardErr("UrlTest", &err)()
	log.Debugf("ios_exports", "UrlTest begin url=%s standard=%d", url, standard)
	ret, err = healthcheck.URLTest(url, standard)
	if err != nil {
		log.Debugf("ios_exports", "UrlTest failed url=%s standard=%d err=%v", url, standard, err)
		return ret, err
	}
	log.Debugf("ios_exports", "UrlTest OK url=%s standard=%d ms=%d", url, standard, ret)
	return ret, nil
}

func CheckServerAlive(address string, port int) (status int32) {
	status = -1
	defer guardStatus("CheckServerAlive", &status)()
	log.Debugf("ios_exports", "CheckServerAlive begin address=%s port=%d", address, port)
	res := healthcheck.CheckServerAlive(address, port)
	log.Debugf("ealth", "heck result: %v", res)
	if res == nil {
		log.Debugf("ios_exports", "CheckServerAlive OK address=%s port=%d", address, port)
		return 0
	}
	log.Debugf("ios_exports", "CheckServerAlive failed address=%s port=%d err=%v", address, port, res)
	return -1
}

func GetConnectionState() int32 {
	switch healthcheck.GetConnectionState() {
	case healthcheck.Disconnected:
		return 0
	case healthcheck.Connecting:
		return 1
	case healthcheck.Connected:
		return 2
	default:
		return 0
	}
}

func InitHealthCheck() {
	log.Debugf("ios_exports", "Init health check")
	healthcheck.InitHealthCheck()
}

func StartHealthCheck() {
	log.Debugf("ios_exports", "Start health check")
	healthcheck.StartHealthCheck()
}

func StopHealthCheck() {
	log.Debugf("ios_exports", "Stop health check")
	healthcheck.StopHealthCheck()
}
