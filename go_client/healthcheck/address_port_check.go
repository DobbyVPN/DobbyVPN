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
		Timeout:   1 * time.Second,
		KeepAlive: -1,
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

		func() {
			defer conn.Close()

			_ = conn.SetDeadline(time.Now().Add(1 * time.Second))

			if _, err := conn.Write([]byte{0x00}); err != nil {
				lastErr = err
				return
			}

			buf := make([]byte, 1)
			_, err = conn.Read(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					lastErr = nil
					return
				}

				lastErr = err
				return
			}

			lastErr = nil
		}()

		if lastErr == nil {
			return nil
		}
	}

	return lastErr
}
