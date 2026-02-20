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
			defer func() { _ = conn.Close() }()

			_ = conn.SetDeadline(time.Now().Add(1 * time.Second))

			// We intentionally perform both Write and Read in addition to Dial.
			//
			// On iOS and mobile networks, a successful TCP connect (Dial) does NOT
			// necessarily mean that the remote service is actually listening.
			// The connection may be accepted optimistically by a proxy, NAT, or
			// SYN-proxy before reaching the real server.
			//
			// Write() alone is also insufficient: it only confirms that the OS
			// accepted data into a local send buffer, not that the server received it.
			//
			// Read() forces TCP to resolve the real connection state:
			//   - timeout  -> the server is alive but silent (acceptable)
			//   - data     -> the server is alive and responding
			//   - EOF/RST  -> the connection was not actually accepted by the service
			//
			// This sequence minimizes false-positive "alive" results caused by
			// optimistic TCP connections.

			if _, writeErr := conn.Write([]byte{0x00}); writeErr != nil {
				lastErr = writeErr
				return
			}

			buf := make([]byte, 1)
			_, readErr := conn.Read(buf)
			if readErr != nil {
				if ne, ok := readErr.(net.Error); ok && ne.Timeout() {
					lastErr = nil
					return
				}

				lastErr = readErr
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
