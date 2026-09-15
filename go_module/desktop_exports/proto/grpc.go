//go:build !(android || ios)

package proto

import (
	"go_module/grpcproto"
	"go_module/sessionapi"
	"go_module/sessionapi/grpctransport"
)

type Server struct {
	grpcproto.UnimplementedVpnServer
	sessions *grpctransport.Handler
}

// NewServer permits desktop tests and embedders to inject the process session
// manager. The zero-value Server remains supported for existing executors.
func NewServer(manager *sessionapi.Manager) *Server {
	if manager == nil {
		return &Server{}
	}
	return &Server{sessions: grpctransport.New(manager)}
}
