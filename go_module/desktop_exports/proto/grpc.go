//go:build !(android || ios)

package proto

import (
	"go_module/grpcproto"
	"go_module/sessionapi/v1"
)

type Server struct {
	grpcproto.UnimplementedVpnServer
	sessions *sessionHost
}

// NewServer permits desktop tests and embedders to inject the process session
// manager. The zero-value Server remains supported for existing executors.
func NewServer(manager *v1.Manager) *Server {
	if manager == nil {
		return &Server{}
	}
	return &Server{sessions: newSessionHost(manager)}
}
