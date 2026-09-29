package controljson

import (
	"errors"
	"log"
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
				log.Printf("desktop JSON client rejected: %v", err)
				_ = conn.Close()
				continue
			}
		}
		go func() {
			if err := handler.ServeConn(conn); err != nil {
				log.Printf("desktop JSON request failed: %v", err)
			}
		}()
	}
}
