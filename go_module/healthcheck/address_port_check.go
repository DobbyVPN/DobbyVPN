package healthcheck

import (
	"errors"
	"go_module/log"
	"net"
	"strconv"
	"strings"
	"time"
)

func CheckServerAlive(address string, port int) error {
	target := net.JoinHostPort(address, strconv.Itoa(port))
	start := time.Now()
	log.Infof("[HealthCheck] CheckServerAlive begin target=%s attempts=3 timeout=1s", target)

	d := net.Dialer{
		Timeout:   1 * time.Second,
		KeepAlive: -1,
	}

	var lastErr error

	for i := 0; i < 3; i++ {
		attemptStart := time.Now()
		log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s dial_begin", i+1, target)
		conn, err := d.Dial("tcp", target)
		if err != nil {
			lastErr = err
			log.Infof(
				"[HealthCheck] CheckServerAlive attempt=%d target=%s dial_failed elapsedMs=%d err=%v",
				i+1,
				target,
				time.Since(attemptStart).Milliseconds(),
				err,
			)
			if strings.Contains(err.Error(), "refused") {
				log.Infof("[HealthCheck] CheckServerAlive target=%s refusing immediately err=%v", target, err)
				return err
			}
			continue
		}
		log.Infof(
			"[HealthCheck] CheckServerAlive attempt=%d target=%s dial_ok local=%s remote=%s elapsedMs=%d",
			i+1,
			target,
			conn.LocalAddr(),
			conn.RemoteAddr(),
			time.Since(attemptStart).Milliseconds(),
		)

		func() {
			defer func() {
				if closeErr := conn.Close(); closeErr != nil {
					log.Infof(
						"[HealthCheck] CheckServerAlive attempt=%d target=%s close_failed err=%v",
						i+1,
						target,
						closeErr,
					)
				} else {
					log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s close_ok", i+1, target)
				}
			}()

			if err := conn.SetDeadline(time.Now().Add(1 * time.Second)); err != nil {
				lastErr = err
				log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s set_deadline_failed err=%v", i+1, target, err)
				return
			}
			log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s write_probe_begin bytes=1", i+1, target)

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
				log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s write_probe_failed err=%v", i+1, target, writeErr)
				return
			}
			log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s write_probe_ok read_probe_begin bytes=1", i+1, target)

			buf := make([]byte, 1)
			n, readErr := conn.Read(buf)
			if readErr != nil {
				var ne net.Error
				if errors.As(readErr, &ne) && ne.Timeout() {
					lastErr = nil
					log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s read_probe_timeout_treated_alive", i+1, target)
					return
				}

				lastErr = readErr
				log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s read_probe_failed err=%v", i+1, target, readErr)
				return
			}

			lastErr = nil
			log.Infof("[HealthCheck] CheckServerAlive attempt=%d target=%s read_probe_ok bytes=%d", i+1, target, n)
		}()

		if lastErr == nil {
			log.Infof(
				"[HealthCheck] CheckServerAlive OK target=%s attempt=%d totalElapsedMs=%d",
				target,
				i+1,
				time.Since(start).Milliseconds(),
			)
			return nil
		}
	}

	log.Infof("[HealthCheck] CheckServerAlive failed target=%s totalElapsedMs=%d lastErr=%v", target, time.Since(start).Milliseconds(), lastErr)
	return lastErr
}
