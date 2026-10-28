package outline

import (
	"context"
	"errors"
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
	closed   bool
	workers  sync.WaitGroup
	client   *statute.AddrSpec
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
		if closeErr := relay.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			log.Debugf(Category, "[SOCKS5 UDP] relay close failed: %v", closeErr)
		}
	}()

	if err := socks5.SendReply(writer, statute.RepSuccess, relay.LocalAddr()); err != nil {
		return fmt.Errorf("failed to send reply: %w", err)
	}
	log.Debugf(Category, "[SOCKS5 UDP] association open relay=%s client=%s", relay.LocalAddr(), request.RemoteAddr)

	associationCtx, finish := d.dialContext(ctx)
	association := &udpAssociation{device: d, relay: relay, ctx: associationCtx, upstream: map[string]net.Conn{}, client: request.DestAddr}
	// The upstream server supplies a background request context. Device or request
	// cancellation must also unblock the control read and the relay read.
	callbackDone := make(chan struct{})
	stopCallback := context.AfterFunc(associationCtx, func() {
		defer close(callbackDone)
		if err := relay.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Warnf(Category, "[SOCKS5 UDP] cancel relay close failed: %v", err)
		}
		if control, ok := writer.(io.Closer); ok {
			if err := control.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Warnf(Category, "[SOCKS5 UDP] cancel control close failed: %v", err)
			}
		}
	})
	defer func() {
		if !stopCallback() {
			<-callbackDone
		}
		finish()
		association.closeUpstreams()
		_ = relay.Close()
		association.workers.Wait()
	}()
	association.workers.Add(1)
	if err := d.Submit(func() { defer association.workers.Done(); association.relayFromClient() }); err != nil {
		association.workers.Done()
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
		if a.client == nil || (!a.client.IP.IsUnspecified() && !a.client.IP.Equal(client.IP)) || (a.client.Port != 0 && a.client.Port != client.Port) {
			continue
		}
		if read < 3 || buffer[0] != 0 || buffer[1] != 0 || buffer[2] != 0 {
			continue
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
			logUDPFailure("write to "+destination, err)
			a.dropUpstream(key, upstream)
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
		return nil
	}

	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		_ = upstream.Close()
		return nil
	}
	if existing, ok := a.upstream[key]; ok {
		a.mu.Unlock()
		if closeErr := upstream.Close(); closeErr != nil {
			log.Debugf(Category, "[SOCKS5 UDP] duplicate upstream close failed: %v", closeErr)
		}
		return existing
	}
	a.upstream[key] = upstream
	a.workers.Add(1)
	a.mu.Unlock()

	if err := a.device.Submit(func() { defer a.workers.Done(); a.relayToClient(key, upstream, client, header) }); err != nil {
		a.workers.Done()
		log.Debugf(Category, "[SOCKS5 UDP] submit reply worker for %s failed: %v", destination, err)
		a.dropUpstream(key, upstream)
		return nil
	}
	return upstream
}

func (a *udpAssociation) relayToClient(key string, upstream net.Conn, client *net.UDPAddr, header []byte) {
	defer a.dropUpstream(key, upstream)
	buffer := make([]byte, maxDatagramSize)
	reply := make([]byte, 0, maxDatagramSize+len(header))
	for {
		read, err := upstream.Read(buffer)
		if err != nil {
			logUDPFailure("read from "+key, err)
			return
		}
		reply = append(append(reply[:0], header...), buffer[:read]...)
		if _, err := a.relay.WriteToUDP(reply, client); err != nil {
			logUDPFailure("write to client", err)
			return
		}
	}
}

func (a *udpAssociation) dropUpstream(key string, upstream net.Conn) {
	a.mu.Lock()
	current, ok := a.upstream[key]
	ok = ok && current == upstream
	if ok {
		delete(a.upstream, key)
	}
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
	a.closed = true
	upstreams := a.upstream
	a.upstream = map[string]net.Conn{}
	a.mu.Unlock()
	for _, upstream := range upstreams {
		if err := upstream.Close(); err != nil {
			log.Debugf(Category, "[SOCKS5 UDP] upstream close failed: %v", err)
		}
	}
}

func logUDPFailure(operation string, err error) {
	if errors.Is(err, io.EOF) || endsConnectionRoutinely(err) {
		log.Debugf(Category, "[SOCKS5 UDP] %s ended: %v", operation, err)
	} else {
		log.Warnf(Category, "[SOCKS5 UDP] %s failed: %v", operation, err)
	}
}
