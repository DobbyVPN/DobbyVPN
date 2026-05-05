package healthcheck

import (
	"context"
	"github.com/matsuridayo/libneko/speedtest"
	"go_module/log"
	"go_module/tunnel/protected_dialer"
	"time"
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

func ProtectedTCPPing(address string) (int32, error) {
	start := time.Now()
	log.Infof("[HealthCheck] ProtectedTCPPing begin address=%s timeoutMs=%d", address, pingTimeoutMilliseconds)
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeoutMilliseconds*time.Millisecond)
	defer cancel()

	conn, err := protected_dialer.DialContextWithProtect(ctx, "tcp", address)
	if err != nil {
		log.Infof("[HealthCheck] ProtectedTCPPing failed address=%s elapsedMs=%d err=%v", address, time.Since(start).Milliseconds(), err)
		return -1, err
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			log.Infof("[HealthCheck] ProtectedTCPPing close_failed address=%s err=%v", address, closeErr)
		} else {
			log.Infof("[HealthCheck] ProtectedTCPPing close_ok address=%s", address)
		}
	}()

	elapsed := int32(time.Since(start).Milliseconds())
	log.Infof("[HealthCheck] ProtectedTCPPing OK address=%s local=%s remote=%s elapsedMs=%d", address, conn.LocalAddr(), conn.RemoteAddr(), elapsed)
	return elapsed, nil
}
