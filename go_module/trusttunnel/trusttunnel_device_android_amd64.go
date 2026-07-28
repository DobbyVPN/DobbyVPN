//go:build android && amd64

package trusttunnel

import (
	"errors"
	"net"
)

// androidAMD64Unsupported explains why this deliberately narrow test ABI does
// not expose TrustTunnel.  The production arm64-v8a build retains the native
// bridge implementation; returning a normal protocol error here keeps an
// emulator-only limitation from becoming an application-load failure.
var androidAMD64Unsupported = errors.New("TrustTunnel is not available on Android x86_64: its native bridge is packaged for arm64-v8a only")

// TrustTunnelDevice retains the protocol-device shape so callers receive a
// correlated start failure instead of an ABI-dependent linker crash.
type TrustTunnelDevice struct{}

func NewTrustTunnelDevice(string) (*TrustTunnelDevice, error) {
	return nil, androidAMD64Unsupported
}

func (*TrustTunnelDevice) Open(int, string) error {
	return androidAMD64Unsupported
}

func (*TrustTunnelDevice) GetProxyAddr() string { return "" }

func (*TrustTunnelDevice) GetServerIP() net.IP { return nil }

func (*TrustTunnelDevice) Close() error { return nil }
