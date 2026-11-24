package xray

import (
	"io"
	"reflect"
	"sync"
	"unsafe"

	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/splithttp"
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

// retiredGRPCClients holds connections a transport reset took out of
// xray-core's map. New streams no longer use them; streams already on them
// keep running. They are closed only when the device closes: connectivity
// state is no proof that no stream runs (after a server GOAWAY grpc-go reports
// Idle while the old transport still drains its streams). An unused retired
// connection releases its socket by itself through grpc-go's default 30 min
// channel idle timeout. Guarded by xrayGRPCClientsLock.
var retiredGRPCClients []*grpc.ClientConn

// retireXrayGRPCClients makes the next gRPC streams dial new connections
// without breaking streams that are still running on the current ones.
func retireXrayGRPCClients() (retired int) {
	xrayGRPCClientsLock.Lock()
	defer xrayGRPCClientsLock.Unlock()
	for key, conn := range xrayGRPCClients {
		delete(xrayGRPCClients, key)
		if conn != nil {
			retiredGRPCClients = append(retiredGRPCClients, conn)
			retired++
		}
	}
	return retired
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

// retiredXHTTPManagers holds XMUX managers a transport reset took out of
// xray-core's map. They are closed only when the device closes: XmuxClient's
// Running counter is no proof that no request runs, because a packet-up
// connection can upload through a replacement client it never counted.
// Guarded by xrayXHTTPClientsLock.
var retiredXHTTPManagers []*splithttp.XmuxManager

// xmuxClientsOffset locates XmuxManager's unexported client list; ok is false
// if xray-core changed it, and the Torturer test guards it.
func xmuxClientsOffset() (offset uintptr, ok bool) {
	field, found := reflect.TypeFor[splithttp.XmuxManager]().FieldByName("xmuxClients")
	return field.Offset, found && field.Type == reflect.TypeFor[[]*splithttp.XmuxClient]()
}

// retireXrayXHTTPClients makes the next XHTTP streams dial new connections
// without closing the current ones, so running requests are not cut.
func retireXrayXHTTPClients() (retired int) {
	xrayXHTTPClientsLock.Lock()
	defer xrayXHTTPClientsLock.Unlock()
	for key, manager := range xrayXHTTPClients {
		delete(xrayXHTTPClients, key)
		if manager != nil {
			retiredXHTTPManagers = append(retiredXHTTPManagers, manager)
			retired++
		}
	}
	return retired
}

// closeXrayXHTTPClients closes every XHTTP client xray-core has created,
// including retired ones (an HTTP/3 transport holds a QUIC connection that
// its keepalive would otherwise keep open). Called after the instance closed,
// when no request can run any more.
func closeXrayXHTTPClients() {
	xrayXHTTPClientsLock.Lock()
	defer xrayXHTTPClientsLock.Unlock()
	managers := retiredXHTTPManagers
	for _, manager := range xrayXHTTPClients {
		managers = append(managers, manager)
	}
	clear(xrayXHTTPClients)
	retiredXHTTPManagers = nil
	offset, ok := xmuxClientsOffset()
	if !ok {
		return
	}
	for _, manager := range managers {
		if manager == nil {
			continue
		}
		// #nosec G103 -- reads the client list at the offset checked by reflection above.
		for _, client := range *(*[]*splithttp.XmuxClient)(unsafe.Add(unsafe.Pointer(manager), offset)) {
			client.NotUsed.Store(true)
			if closer, ok := client.XmuxConn.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}
}
