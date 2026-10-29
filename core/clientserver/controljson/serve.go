package controljson

import (
	"errors"
	"io"
	"net"
	"syscall"

	"core/log"
)

type AuthorizeConn func(net.Conn) error

// routineDisconnect requires every leaf of a joined error to describe a
// normal peer shutdown. A routine error must not mask a separate failure.
func routineDisconnect(err error) bool {
	if err == nil {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !routineDisconnect(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return routineDisconnect(wrapped.Unwrap())
	}
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) || isPlatformRoutineDisconnect(err)
}

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
				fields := map[string]any{"error": err.Error()}
				if routineDisconnect(err) {
					log.Debug("CONTROL", "desktop JSON request failed", fields)
				} else {
					log.Warn("CONTROL", "desktop JSON request failed", fields)
				}
			}
		}()
	}
}
