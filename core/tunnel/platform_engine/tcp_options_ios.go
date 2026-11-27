//go:build ios

package platform_engine

import (
	"github.com/xjasonlyu/tun2socks/v2/core/option"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
)

const iosTCPBufferSize = 32 << 10

var tcpStackOptions = []option.Option{
	option.WithTCPSendBufferSizeRange(tcp.MinBufferSize, iosTCPBufferSize, iosTCPBufferSize),
	option.WithTCPReceiveBufferSizeRange(tcp.MinBufferSize, iosTCPBufferSize, iosTCPBufferSize),
	option.WithTCPModerateReceiveBuffer(false),
}
