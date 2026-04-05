package healthcheck

import (
	"github.com/matsuridayo/libneko/speedtest"
)

const (
	pingTimeoutMilliseconds = 1000
)

func TCPPing(address string) (int32, error) {
	return speedtest.TcpPing(address, pingTimeoutMilliseconds)
}
