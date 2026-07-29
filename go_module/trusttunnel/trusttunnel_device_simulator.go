//go:build ios && simulator

package trusttunnel

import (
	"errors"
	"net"
)

// simulatorUnsupported keeps the public iOS Simulator build loadable while
// making the native-bridge limitation explicit to the normal protocol
// lifecycle. The physical iOS implementation remains the only one that can
// start TrustTunnel.
var simulatorUnsupported = errors.New("TrustTunnel is not available on iOS Simulator: its native bridge is packaged for physical iOS only")

// TrustTunnelDevice retains the ProtocolDevice shape. Returning a normal
// construction error lets the shared Go session manager report a correlated
// start failure instead of making the Simulator app fail to link or load.
type TrustTunnelDevice struct{}

func NewTrustTunnelDevice(string) (*TrustTunnelDevice, error) {
	return nil, simulatorUnsupported
}

func (*TrustTunnelDevice) Open(int, string) error { return simulatorUnsupported }

func (*TrustTunnelDevice) GetProxyAddr() string { return "" }

func (*TrustTunnelDevice) GetServerIP() net.IP { return nil }

func (*TrustTunnelDevice) Close() error { return nil }
