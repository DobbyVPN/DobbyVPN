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
		Timeout: 1 * time.Second,
	}

	var lastErr error

	for i := 0; i < 3; i++ {
		conn, err := d.Dial("tcp", target)
		if err != nil {
			lastErr = err
			if strings.Contains(err.Error(), "refused") {
				return err
			}
			continue
		}

		defer conn.Close()

		_ = conn.SetDeadline(time.Now().Add(1 * time.Second))

		_, err = conn.Write([]byte{0x00})
		if err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	return lastErr
}
