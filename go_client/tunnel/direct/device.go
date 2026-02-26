package direct

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/icmp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
	"gvisor.dev/gvisor/pkg/waiter"
)

type DirectIPDevice struct {
	ep *channel.Endpoint
	s  *stack.Stack
}

func NewDirectIPDevice() (*DirectIPDevice, error) {
	// Инициализируем стек gVisor с нужными протоколами
	opts := stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol, ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol, icmp.NewProtocol4, icmp.NewProtocol6},
	}
	s := stack.New(opts)

	// Создаем виртуальный интерфейс. MTU 1500
	const mtu = 1500
	ep := channel.New(1024, uint32(mtu), "")

	tcpipErr := s.CreateNIC(1, ep)
	if tcpipErr != nil {
		return nil, fmt.Errorf("CreateNIC: %v", tcpipErr)
	}
	s.SetPromiscuousMode(1, true)
	// Добавляем маршруты для захвата всего трафика (default routes)
	s.AddRoute(tcpip.Route{Destination: header.IPv4EmptySubnet, NIC: 1})
	s.AddRoute(tcpip.Route{Destination: header.IPv6EmptySubnet, NIC: 1})

	// Настраиваем перехват TCP с прозрачным проксированием
	tcpFwd := tcp.NewForwarder(s, 0, 65535, func(r *tcp.ForwarderRequest) {
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			r.Complete(true) // reject the connection
			return
		}
		r.Complete(false)

		conn := gonet.NewTCPConn(&wq, ep)

		// Узнаем, куда шел пакет и звоним туда через реальную ОС
		target := fmt.Sprintf("%s:%d", r.ID().LocalAddress.String(), r.ID().LocalPort)
		out, err := net.Dial("tcp", target)
		if err != nil {
			conn.Close()
			return
		}

		go func() {
			defer conn.Close()
			defer out.Close()
			io.Copy(out, conn)
		}()
		go func() {
			defer conn.Close()
			defer out.Close()
			io.Copy(conn, out)
		}()
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	// Настраиваем перехват UDP с прозрачным проксированием
	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		var wq waiter.Queue
		ep, err := r.CreateEndpoint(&wq)
		if err != nil {
			return
		}

		conn := gonet.NewUDPConn(s, &wq, ep)

		// Звоним в реальный UDP
		target := fmt.Sprintf("%s:%d", r.ID().LocalAddress.String(), r.ID().LocalPort)
		out, err := net.Dial("udp", target)
		if err != nil {
			conn.Close()
			return
		}

		go func() {
			defer conn.Close()
			defer out.Close()
			io.Copy(out, conn)
		}()
		go func() {
			defer conn.Close()
			defer out.Close()
			io.Copy(conn, out)
		}()
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	return &DirectIPDevice{
		ep: ep,
		s:  s,
	}, nil
}

// Запись пакетов из туннеля в gVisor
func (d *DirectIPDevice) Write(pkt []byte) (int, error) {
	if len(pkt) == 0 {
		return 0, nil
	}

	// Определяем версию IP по первому байту пакета
	var proto tcpip.NetworkProtocolNumber
	switch pkt[0] >> 4 {
	case 4:
		proto = ipv4.ProtocolNumber
	case 6:
		proto = ipv6.ProtocolNumber
	default:
		return 0, fmt.Errorf("unknown IP version")
	}

	pkb := stack.NewPacketBuffer(stack.PacketBufferOptions{
		Payload: buffer.MakeWithData(pkt),
	})
	d.ep.InjectInbound(proto, pkb)

	return len(pkt), nil
}

// Чтение ответов от gVisor, которые пойдут обратно в туннель
func (d *DirectIPDevice) Read(buf []byte) (int, error) {
	pkt := d.ep.ReadContext(context.Background())
	if pkt.IsNil() {
		return 0, os.ErrClosed
	}
	defer pkt.DecRef()

	// ToView копирует/собирает данные из пакета (заголовки + тело)
	view := pkt.ToView()
	n := copy(buf, view)

	return n, nil
}

func (d *DirectIPDevice) Close() error {
	d.ep.Close()
	return nil
}
