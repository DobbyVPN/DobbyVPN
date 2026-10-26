package outline

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/things-go/go-socks5"
	"github.com/things-go/go-socks5/statute"

	"core/log"
)

// A datagram cannot exceed the largest UDP payload, so one buffer of that size
// always holds a complete one.
const maxDatagramSize = 64 * 1024

// udpAssociation relays datagrams for one client until its control connection
// ends, which is when RFC 1928 says the association terminates.
//
// It replaces the library's own handler, which shares a single relay socket
// between every destination and closes it as soon as one datagram cannot be
// delivered. A client then writes to a port that no longer exists and sees
// every later datagram refused, so one unreachable destination takes down the
// whole association. Here a destination that fails costs only its own
// datagrams, and the next one redials it.
type udpAssociation struct {
	device   *OutlineDevice
	relay    *net.UDPConn
	ctx      context.Context
	mu       sync.Mutex
	upstream map[string]net.Conn
}

func (d *OutlineDevice) handleAssociate(ctx context.Context, writer io.Writer, request *socks5.Request) error {
	control, ok := request.LocalAddr.(*net.TCPAddr)
	if !ok {
		if err := socks5.SendReply(writer, statute.RepServerFailure, nil); err != nil {
			return fmt.Errorf("failed to send reply: %w", err)
		}
		return fmt.Errorf("UDP associate needs a TCP control connection, got %T", request.LocalAddr)
	}

	relay, err := net.ListenUDP("udp", &net.UDPAddr{IP: control.IP})
	if err != nil {
		if replyErr := socks5.SendReply(writer, statute.RepServerFailure, nil); replyErr != nil {
			return fmt.Errorf("failed to send reply: %w", replyErr)
		}
		return fmt.Errorf("listen UDP relay: %w", err)
	}
	defer func() {
		if closeErr := relay.Close(); closeErr != nil {
			log.Debugf(Category, "[SOCKS5 UDP] relay close failed: %v", closeErr)
		}
	}()

	if err := socks5.SendReply(writer, statute.RepSuccess, relay.LocalAddr()); err != nil {
		return fmt.Errorf("failed to send reply: %w", err)
	}
	log.Debugf(Category, "[SOCKS5 UDP] association open relay=%s client=%s", relay.LocalAddr(), request.RemoteAddr)

	association := &udpAssociation{device: d, relay: relay, ctx: ctx, upstream: map[string]net.Conn{}}
	defer association.closeUpstreams()
	if err := d.Submit(association.relayFromClient); err != nil {
		return fmt.Errorf("submit UDP relay worker: %w", err)
	}

	// Hold the association open for as long as its control connection lives.
	// Reading it also reports the moment the client goes away.
	if _, err := io.Copy(io.Discard, request.Reader); err != nil {
		log.Debugf(Category, "[SOCKS5 UDP] association control connection ended: %v", err)
	}
	log.Debugf(Category, "[SOCKS5 UDP] association closed relay=%s", relay.LocalAddr())
	return nil
}

func (a *udpAssociation) relayFromClient() {
	buffer := make([]byte, maxDatagramSize)
	for {
		read, client, err := a.relay.ReadFromUDP(buffer)
		if err != nil {
			return // The association closed this relay.
		}
		packet, err := statute.ParseDatagram(buffer[:read])
		if err != nil {
			log.Debugf(Category, "[SOCKS5 UDP] malformed datagram from %s: %v", client, err)
			continue
		}
		destination := packet.DstAddr.String()
		key := client.String() + "|" + destination
		upstream := a.upstreamFor(key, destination, client, packet.Header())
		if upstream == nil {
			continue // Only this datagram is lost; the association stays open.
		}
		if _, err := upstream.Write(packet.Data); err != nil {
			log.Debugf(Category, "[SOCKS5 UDP] write to %s failed: %v", destination, err)
			a.dropUpstream(key)
		}
	}
}

// upstreamFor returns the connection for one client and destination, dialing
// it on first use. A failed dial returns nil rather than ending the relay.
func (a *udpAssociation) upstreamFor(key, destination string, client *net.UDPAddr, header []byte) net.Conn {
	a.mu.Lock()
	existing, ok := a.upstream[key]
	a.mu.Unlock()
	if ok {
		return existing
	}

	upstream, err := a.device.handleDial(a.ctx, "udp", destination)
	if err != nil {
		log.Debugf(Category, "[SOCKS5 UDP] dial %s failed: %v", destination, err)
		return nil
	}

	a.mu.Lock()
	if existing, ok := a.upstream[key]; ok {
		a.mu.Unlock()
		if closeErr := upstream.Close(); closeErr != nil {
			log.Debugf(Category, "[SOCKS5 UDP] duplicate upstream close failed: %v", closeErr)
		}
		return existing
	}
	a.upstream[key] = upstream
	a.mu.Unlock()

	if err := a.device.Submit(func() { a.relayToClient(key, upstream, client, header) }); err != nil {
		log.Debugf(Category, "[SOCKS5 UDP] submit reply worker for %s failed: %v", destination, err)
		a.dropUpstream(key)
		return nil
	}
	return upstream
}

func (a *udpAssociation) relayToClient(key string, upstream net.Conn, client *net.UDPAddr, header []byte) {
	defer a.dropUpstream(key)
	buffer := make([]byte, maxDatagramSize)
	reply := make([]byte, 0, maxDatagramSize+len(header))
	for {
		read, err := upstream.Read(buffer)
		if err != nil {
			return
		}
		reply = append(append(reply[:0], header...), buffer[:read]...)
		if _, err := a.relay.WriteToUDP(reply, client); err != nil {
			return
		}
	}
}

func (a *udpAssociation) dropUpstream(key string) {
	a.mu.Lock()
	upstream, ok := a.upstream[key]
	delete(a.upstream, key)
	a.mu.Unlock()
	if !ok {
		return
	}
	if err := upstream.Close(); err != nil {
		log.Debugf(Category, "[SOCKS5 UDP] upstream close failed: %v", err)
	}
}

func (a *udpAssociation) closeUpstreams() {
	a.mu.Lock()
	upstreams := a.upstream
	a.upstream = map[string]net.Conn{}
	a.mu.Unlock()
	for _, upstream := range upstreams {
		if err := upstream.Close(); err != nil {
			log.Debugf(Category, "[SOCKS5 UDP] upstream close failed: %v", err)
		}
	}
}
