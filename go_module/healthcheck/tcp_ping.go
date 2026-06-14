package healthcheck

import (
	"time"

	"github.com/matsuridayo/libneko/speedtest"

	"go_module/healthcheck/common"
	"go_module/log"
)

const (
	pingTimeoutMilliseconds = 1000
)

func TCPPing(address string) (int32, error) {
	start := time.Now()
	log.Debugf(common.Category, "TCPPing begin address=%s timeoutMs=%d", address, pingTimeoutMilliseconds)
	ret, err := speedtest.TcpPing(address, pingTimeoutMilliseconds)
	if err != nil {
		log.Debugf(common.Category, "TCPPing failed address=%s elapsedMs=%d err=%v", address, time.Since(start).Milliseconds(), err)
		return ret, err
	}
	log.Debugf(common.Category, "TCPPing OK address=%s retMs=%d elapsedMs=%d", address, ret, time.Since(start).Milliseconds())
	return ret, nil
}
