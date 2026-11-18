package xray

import (
	"sync"
	_ "unsafe" // go:linkname

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	"google.golang.org/grpc"
)

// xray-core keeps every gRPC ClientConn in a package-level map and never closes
// them, so after the instance is closed they keep reconnecting in the
// background. The key type mirrors xray-core's unexported dialerConf
// (net.Destination and *internet.MemoryStreamConfig embedded) so the map can be
// walked here; the Torturer test guards both the symbols and this layout.

//go:linkname xrayGRPCClients github.com/xtls/xray-core/transport/internet/grpc.globalDialerMap
var xrayGRPCClients map[struct {
	net.Destination
	*internet.MemoryStreamConfig
}]*grpc.ClientConn

//go:linkname xrayGRPCClientsLock github.com/xtls/xray-core/transport/internet/grpc.globalDialerAccess
var xrayGRPCClientsLock sync.Mutex

// closeXrayGRPCClients closes all gRPC connections xray-core has created.
func closeXrayGRPCClients() {
	xrayGRPCClientsLock.Lock()
	defer xrayGRPCClientsLock.Unlock()
	for _, conn := range xrayGRPCClients {
		if conn != nil {
			_ = conn.Close()
		}
	}
	clear(xrayGRPCClients)
}
