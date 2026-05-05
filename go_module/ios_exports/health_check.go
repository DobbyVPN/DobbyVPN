package cloak_outline

import (
	"go_module/healthcheck"
	"go_module/log"
)

func TcpPing(address string) (ret int32, err error) {
	defer guardErr("TcpPing", &err)()
	log.Infof("[ios_exports] TcpPing begin address=%s", address)
	ret, err = healthcheck.TCPPing(address)
	if err != nil {
		log.Infof("[ios_exports] TcpPing failed address=%s err=%v", address, err)
		return ret, err
	}
	log.Infof("[ios_exports] TcpPing OK address=%s ms=%d", address, ret)
	return ret, nil
}

func ProtectedTcpPing(address string) (ret int32, err error) {
	defer guardErr("ProtectedTcpPing", &err)()
	log.Infof("[ios_exports] ProtectedTcpPing begin address=%s", address)
	ret, err = healthcheck.ProtectedTCPPing(address)
	if err != nil {
		log.Infof("[ios_exports] ProtectedTcpPing failed address=%s err=%v", address, err)
		return ret, err
	}
	log.Infof("[ios_exports] ProtectedTcpPing OK address=%s ms=%d", address, ret)
	return ret, nil
}

func UrlTest(url string, standard int) (ret int32, err error) {
	defer guardErr("UrlTest", &err)()
	log.Infof("[ios_exports] UrlTest begin url=%s standard=%d", url, standard)
	ret, err = healthcheck.URLTest(url, standard)
	if err != nil {
		log.Infof("[ios_exports] UrlTest failed url=%s standard=%d err=%v", url, standard, err)
		return ret, err
	}
	log.Infof("[ios_exports] UrlTest OK url=%s standard=%d ms=%d", url, standard, ret)
	return ret, nil
}

func CheckServerAlive(address string, port int) (status int32) {
	status = -1
	defer guardStatus("CheckServerAlive", &status)()
	log.Infof("[ios_exports] CheckServerAlive begin address=%s port=%d", address, port)
	res := healthcheck.CheckServerAlive(address, port)
	log.Infof("Health check result: %v", res)
	if res == nil {
		log.Infof("[ios_exports] CheckServerAlive OK address=%s port=%d", address, port)
		return 0
	}
	log.Infof("[ios_exports] CheckServerAlive failed address=%s port=%d err=%v", address, port, res)
	return -1
}
