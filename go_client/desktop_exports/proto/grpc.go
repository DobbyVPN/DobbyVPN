package proto

import (
	"go_client/grpcproto"
)

type Server struct {
	grpcproto.UnimplementedVpnServer
}
