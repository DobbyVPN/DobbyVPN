package outline

import "net"

type Driver interface {
	Connect() error
	Disconnect() error
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Refresh() error
	GetServerIP() net.IP
}
