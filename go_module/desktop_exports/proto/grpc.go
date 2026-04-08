//go:build !(android || ios)

package proto

import (
	"go_module/grpcproto"
)

type Server struct {
	grpcproto.UnimplementedVpnServer
}
