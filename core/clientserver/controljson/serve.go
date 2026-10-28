package controljson

import (
	"core/log"
	"errors"
	"net"
)

type AuthorizeConn func(net.Conn) error

func Serve(listener net.Listener, handler Handler, authorize AuthorizeConn) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		if authorize != nil {
			if err := authorize(conn); err != nil {
				log.Warn("CONTROL", "desktop JSON client rejected", map[string]any{"error": err.Error()})
				_ = conn.Close()
				continue
			}
		}
		go func() {
			if err := handler.ServeConn(conn); err != nil {
				log.Warn("CONTROL", "desktop JSON request failed", map[string]any{"error": err.Error()})
			}
		}()
	}
}
