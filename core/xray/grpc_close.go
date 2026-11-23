package xray

import (
	"reflect"
	"sync"
	"unsafe"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
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

// retiredGRPCClients holds connections a transport reset took out of
// xray-core's map. New streams no longer use them; streams already on them
// keep running. Guarded by xrayGRPCClientsLock.
var retiredGRPCClients []*grpc.ClientConn

// retireXrayGRPCClients makes the next gRPC streams dial new connections
// without breaking streams that are still running on the current ones. A
// retired connection is closed at a later reset once it can carry no stream
// (idle, failed or shut down), and at the latest when the device closes.
func retireXrayGRPCClients() (retired, closed int) {
	xrayGRPCClientsLock.Lock()
	defer xrayGRPCClientsLock.Unlock()
	kept := retiredGRPCClients[:0]
	for _, conn := range retiredGRPCClients {
		switch conn.GetState() {
		case connectivity.Idle, connectivity.TransientFailure, connectivity.Shutdown:
			_ = conn.Close()
			closed++
		case connectivity.Connecting, connectivity.Ready:
			// It may still carry streams; keep it until it is idle or fails.
			kept = append(kept, conn)
		}
	}
	for key, conn := range xrayGRPCClients {
		delete(xrayGRPCClients, key)
		if conn != nil {
			kept = append(kept, conn)
			retired++
		}
	}
	retiredGRPCClients = kept
	return retired, closed
}

// closeXrayGRPCClients closes all gRPC connections xray-core has created,
// including retired ones.
func closeXrayGRPCClients() {
	xrayGRPCClientsLock.Lock()
	defer xrayGRPCClientsLock.Unlock()
	for _, conn := range xrayGRPCClients {
		if conn != nil {
			_ = conn.Close()
		}
	}
	clear(xrayGRPCClients)
	for _, conn := range retiredGRPCClients {
		_ = conn.Close()
	}
	retiredGRPCClients = nil
}

// xray-core keeps XHTTP (splithttp) clients the same way, one XmuxManager per
// destination.

//go:linkname xrayXHTTPClients github.com/xtls/xray-core/transport/internet/splithttp.globalDialerMap
var xrayXHTTPClients map[struct {
	net.Destination
	*internet.MemoryStreamConfig
}]*splithttp.XmuxManager

//go:linkname xrayXHTTPClientsLock github.com/xtls/xray-core/transport/internet/splithttp.globalDialerAccess
var xrayXHTTPClientsLock sync.Mutex

// xmuxClientsOffset locates XmuxManager's unexported client list; ok is false
// if xray-core changed it, and the Torturer test guards it.
func xmuxClientsOffset() (offset uintptr, ok bool) {
	field, found := reflect.TypeFor[splithttp.XmuxManager]().FieldByName("xmuxClients")
	return field.Offset, found && field.Type == reflect.TypeFor[[]*splithttp.XmuxClient]()
}

// retireXrayXHTTPClients makes the next XHTTP streams dial new connections.
// Each old client is marked NotUsed, as xray-core does for a client it stops
// reusing, so xray-core closes its connection (including an HTTP/3 transport)
// once no request runs on it; running requests are not cut.
func retireXrayXHTTPClients() (retired int) {
	xrayXHTTPClientsLock.Lock()
	defer xrayXHTTPClientsLock.Unlock()
	offset, ok := xmuxClientsOffset()
	for key, manager := range xrayXHTTPClients {
		delete(xrayXHTTPClients, key)
		if manager == nil || !ok {
			continue
		}
		// #nosec G103 -- reads the client list at the offset checked by reflection above.
		for _, client := range *(*[]*splithttp.XmuxClient)(unsafe.Add(unsafe.Pointer(manager), offset)) {
			client.NotUsed.Store(true)
			// DoneRunning closes the connection if nothing runs on it.
			client.AddRunning()
			client.DoneRunning()
			retired++
		}
	}
	return retired
}
