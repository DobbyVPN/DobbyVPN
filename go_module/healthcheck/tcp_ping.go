package healthcheck

import (
	"context"
	"time"

	"github.com/matsuridayo/libneko/speedtest"

	"go_module/log"
	"go_module/tunnel/protected_dialer"
)

const (
	pingTimeoutMilliseconds = 1000
)

func TCPPing(address string) (int32, error) {
	start := time.Now()
	log.Infof("[HealthCheck] TCPPing begin address=%s timeoutMs=%d", address, pingTimeoutMilliseconds)
	ret, err := speedtest.TcpPing(address, pingTimeoutMilliseconds)
	if err != nil {
		log.Infof("[HealthCheck] TCPPing failed address=%s elapsedMs=%d err=%v", address, time.Since(start).Milliseconds(), err)
		return ret, err
	}
	log.Infof("[HealthCheck] TCPPing OK address=%s retMs=%d elapsedMs=%d", address, ret, time.Since(start).Milliseconds())
	return ret, nil
}
