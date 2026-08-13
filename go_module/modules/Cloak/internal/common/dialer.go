package common

import (
	"context"
	"net"
)

type Dialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}
