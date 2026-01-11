package healthcheck

import (
	"fmt"
	"net"
	"strings"
	"time"
)

func CheckServerAlive(address string, port int) error {
	target := fmt.Sprintf("%s:%d", address, port)

	d := net.Dialer{
		Timeout: 1000 * time.Millisecond,
	}

	var lastErr error
	for i := 0; i < 3; i++ {
		conn, err := d.Dial("tcp", target)
		if err == nil {
			conn.Close()
			return nil
		}
		lastErr = err
		if strings.Contains(err.Error(), "refused") {
			return err
		}
	}
	return lastErr
}
