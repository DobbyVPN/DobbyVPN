package proto

import (
	"go_module/desktop_exports/controlplane"
	"google.golang.org/grpc"
)

// ControlTokenMetadata is deliberately a non-descriptive metadata key. Its value
// is never logged by this package or the executor.
const ControlTokenMetadata = controlplane.TokenMetadata

// ControlAuthUnaryInterceptor is always installed before application handlers.
// Unix callers are authenticated by a verified peer credential; Windows callers
// must additionally provide the installation token in request metadata.
func ControlAuthUnaryInterceptor(requireToken bool, expectedToken string) grpc.UnaryServerInterceptor {
	return controlplane.UnaryAuthInterceptor(requireToken, expectedToken)
}
