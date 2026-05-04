package healthcheck

import (
	"context"
	"github.com/matsuridayo/libneko/speedtest"
	"go_module/tunnel/protected_dialer"
	"time"
)

const (
	pingTimeoutMilliseconds = 1000
)

func TCPPing(address string) (int32, error) {
	return speedtest.TcpPing(address, pingTimeoutMilliseconds)
}

func ProtectedTCPPing(address string) (int32, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeoutMilliseconds*time.Millisecond)
	defer cancel()

	conn, err := protected_dialer.DialContextWithProtect(ctx, "tcp", address)
	if err != nil {
		return -1, err
	}
	defer conn.Close()

	return int32(time.Since(start).Milliseconds()), nil
}

