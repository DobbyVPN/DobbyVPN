package platform_engine

import (
	"context"
	"fmt"
	"net"
	"sync"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

// PacketFlowDevice connects an OS-owned raw IP packet flow to the Go stack.
// The OS retains interface, route and DNS ownership.
type PacketFlowDevice struct {
	*channel.Endpoint
	mu     sync.RWMutex
	closed bool
}

func NewPacketFlowDevice() *PacketFlowDevice {
	// The outbound queue retains at most 64 MTU-sized packets.
	return &PacketFlowDevice{Endpoint: channel.New(64, TunnelMTU, "")}
}

func (*PacketFlowDevice) Name() string { return "packet-flow" }
func (*PacketFlowDevice) Type() string { return "packet-flow" }

// Receive injects one complete raw IP packet and copies the platform buffer
// before returning. version is the platform-reported IPv4 or IPv6 family.
func (d *PacketFlowDevice) Receive(version uint8, payload []byte) error {
	protocol, err := packetFlowProtocol(version, payload)
	if err != nil {
		return err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return net.ErrClosed
	}
	packet := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(payload)})
	defer packet.DecRef()
	d.InjectInbound(protocol, packet)
	return nil
}

// ReadPacket gives the caller ownership of exactly one outbound packet. Close
// and context cancellation both unblock an idle reader.
func (d *PacketFlowDevice) ReadPacket(ctx context.Context) (uint8, []byte, error) {
	packet := d.ReadContext(ctx)
	if packet == nil {
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		return 0, nil, net.ErrClosed
	}
	defer packet.DecRef()
	view := packet.ToView()
	defer view.Release()
	data := view.AsSlice()
	if len(data) == 0 {
		return 0, nil, fmt.Errorf("empty outbound packet")
	}
	version := data[0] >> 4
	if _, err := packetFlowProtocol(version, data); err != nil {
		return 0, nil, err
	}
	return version, append([]byte(nil), data...), nil
}

func (d *PacketFlowDevice) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	d.closed = true
	d.Attach(nil)
	d.Endpoint.Close()
}

func packetFlowProtocol(version uint8, payload []byte) (tcpip.NetworkProtocolNumber, error) {
	if len(payload) == 0 || len(payload) > TunnelMTU || payload[0]>>4 != version {
		return 0, fmt.Errorf("invalid packet family or length: family=%d size=%d", version, len(payload))
	}
	switch version {
	case 4:
		if len(payload) >= header.IPv4MinimumSize {
			return header.IPv4ProtocolNumber, nil
		}
	case 6:
		if len(payload) >= header.IPv6MinimumSize {
			return header.IPv6ProtocolNumber, nil
		}
	}
	return 0, fmt.Errorf("invalid IPv%d header: size=%d", version, len(payload))
}
