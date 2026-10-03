//go:build linux && !(android || ios)

package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/godbus/dbus/v5"
)

type resolvedAddress struct {
	Family  int32
	Address []byte
}
type resolvedDomain struct {
	Domain      string
	RoutingOnly bool
}

var linuxResolvedCall = callResolved

// The system bus has a fixed local socket. Dial it directly so neither a
// session-bus autolaunch nor an unbounded transport connect is reachable.
func callResolved(parent context.Context, method string, args ...any) (resultErr error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", "/run/dbus/system_bus_socket")
	if err != nil {
		return fmt.Errorf("connect resolved system bus: %w", err)
	}
	conn, err := dbus.NewConn(socket, dbus.WithContext(ctx))
	if err != nil {
		return errors.Join(err, socket.Close())
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close()) }()
	if err := conn.Auth(nil); err != nil {
		return fmt.Errorf("authenticate resolved system bus: %w", err)
	}
	if err := conn.Hello(); err != nil {
		return fmt.Errorf("register resolved system bus connection: %w", err)
	}
	object := conn.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1")
	if err := object.CallWithContext(ctx, "org.freedesktop.resolve1.Manager."+method, 0, args...).Err; err != nil {
		return fmt.Errorf("resolved %s: %w", method, err)
	}
	return nil
}
