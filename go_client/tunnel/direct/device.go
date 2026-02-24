package direct

import (
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
	"syscall"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

type DirectIPDevice struct {
	ep       *channel.Endpoint
	st       *stack.Stack
	mtu      int
	incoming chan *buffer.View

	closed bool
	mu     sync.Mutex
}

// localAddrs – список адресов, которые будут «видеть» приложения за tun.
// mtu – MTU твоего VPN-tun’а.
func NewDirectIPDevice() (*DirectIPDevice, error) {
	localAddrs := []netip.Addr{
		netip.MustParseAddr("198.18.0.1"),
	}
	mtu := 1500
	opts := stack.Options{
		NetworkProtocols: []stack.NetworkProtocolFactory{
			ipv4.NewProtocol,
			ipv6.NewProtocol,
		},
		TransportProtocols: []stack.TransportProtocolFactory{
			tcp.NewProtocol,
			udp.NewProtocol,
			icmp.NewProtocol4,
			icmp.NewProtocol6,
		},
		HandleLocal: true,
	}

	ep := channel.New(1024, uint32(mtu), "")
	st := stack.New(opts)

	dev := &DirectIPDevice{
		ep:       ep,
		st:       st,
		mtu:      mtu,
		incoming: make(chan *buffer.View, 128),
	}

	// Включаем TCP SACK (по умолчанию выключен).
	sack := tcpip.TCPSACKEnabled(true)
	if err := st.SetTransportProtocolOption(tcp.ProtocolNumber, &sack); err != nil {
		return nil, fmt.Errorf("SetTransportProtocolOption(TCPSACK): %w", err)
	}

	// Регистрируем NIC с id = 1.
	if err := st.CreateNIC(1, ep); err != nil {
		return nil, fmt.Errorf("CreateNIC: %w", err)
	}

	var hasV4, hasV6 bool

	// Назначаем адреса.
	for _, ip := range localAddrs {
		var proto tcpip.NetworkProtocolNumber
		switch {
		case ip.Is4():
			proto = ipv4.ProtocolNumber
			hasV4 = true
		case ip.Is6():
			proto = ipv6.ProtocolNumber
			hasV6 = true
		default:
			continue
		}

		protoAddr := tcpip.ProtocolAddress{
			Protocol:          proto,
			AddressWithPrefix: tcpip.AddrFromSlice(ip.AsSlice()).WithPrefix(),
		}

		if err := st.AddProtocolAddress(1, protoAddr, stack.AddressProperties{}); err != nil {
			return nil, fmt.Errorf("AddProtocolAddress(%v): %w", ip, err)
		}
	}

	// Маршруты по умолчанию.
	if hasV4 {
		st.AddRoute(tcpip.Route{
			Destination: header.IPv4EmptySubnet,
			NIC:         1,
		})
	}
	if hasV6 {
		st.AddRoute(tcpip.Route{
			Destination: header.IPv6EmptySubnet,
			NIC:         1,
		})
	}

	// Подписываемся на уведомления, чтобы получать пакеты из стека.
	dev.ep.AddNotify(dev)

	return dev, nil
}

// --- канал уведомлений от channel.Endpoint ---

// WriteNotify вызывается gVisor’ом, когда в ep появляются пакеты для отправки.
func (d *DirectIPDevice) WriteNotify() {
	pkt := d.ep.Read()
	if pkt == nil {
		return
	}

	view := pkt.ToView()
	pkt.DecRef() // ВАЖНО: это PacketBuffer, здесь DecRef есть

	select {
	case d.incoming <- view:
	default:
		// если канал забит, просто дропаем
	}
}

// --- io.ReadWriteCloser интерфейс ---

func (d *DirectIPDevice) Read(p []byte) (int, error) {
	view, ok := <-d.incoming
	if !ok {
		return 0, io.EOF
	}

	// У buffer.View НЕТ DecRef – просто читаем
	n, err := view.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	return n, nil
}

func (d *DirectIPDevice) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(p),
	})

	switch p[0] >> 4 {
	case 4:
		d.ep.InjectInbound(header.IPv4ProtocolNumber, pkb)
	case 6:
		d.ep.InjectInbound(header.IPv6ProtocolNumber, pkb)
	default:
		pkb.DecRef()
		return 0, syscall.EAFNOSUPPORT
	}

	return len(p), nil
}

func (d *DirectIPDevice) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true

	d.st.RemoveNIC(1)
	d.st.Close()
	d.ep.Close()
	close(d.incoming)
	return nil
}
