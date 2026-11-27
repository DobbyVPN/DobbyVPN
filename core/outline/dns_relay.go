package outline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/things-go/go-socks5/statute"

	"core/dnscache"
	"core/log"
)

// Start the TCP exchange after giving UDP a chance to answer. UDP remains
// eligible until either protocol returns a matching response.
const dnsTCPFallbackDelay = 250 * time.Millisecond

func (a *udpAssociation) relayDNS(packet statute.Datagram, destination string, client *net.UDPAddr) bool {
	var query dns.Msg
	if err := query.Unpack(packet.Data); err != nil || query.Response || query.Opcode != dns.OpcodeQuery ||
		len(query.Question) != 1 || query.IsTsig() != nil {
		return false
	}
	select {
	case a.device.dnsRequests <- struct{}{}:
	default:
		return false // Keep ordinary UDP delivery available under load.
	}

	payload, header := bytes.Clone(packet.Data), packet.Header()
	a.workers.Add(1)
	if err := a.device.Submit(func() {
		defer a.workers.Done()
		defer func() { <-a.device.dnsRequests }()
		limit := dns.MinMsgSize
		if edns := query.IsEdns0(); edns != nil {
			// The localhost relay uses IPv4 and also carries the SOCKS header.
			limit = min(max(limit, int(edns.UDPSize())), 65507-len(header))
		}
		response, network, err := resolveRelayDNS(a.ctx, a.device.handleDial, destination, &query, payload, limit)
		if err != nil {
			logUDPFailure("DNS exchange with "+destination, err)
			return
		}
		if network == networkTCP {
			log.Debugf(Category, "[SOCKS5 DNS] reply via TCP fallback resolver=%s", destination)
		}
		if _, err := a.relay.WriteToUDP(append(header, response...), client); err != nil {
			logUDPFailure("write DNS response to client", err)
		}
	}); err != nil {
		a.workers.Done()
		<-a.device.dnsRequests
		logUDPFailure("submit DNS exchange with "+destination, err)
	}
	return true
}

type relayDNSResult struct {
	wire    []byte
	network string
	err     error
}

type relayDNSDialer func(context.Context, string, string) (net.Conn, error)

func resolveRelayDNS(
	parent context.Context, dial relayDNSDialer, destination string, query *dns.Msg, payload []byte, limit int,
) (wire []byte, network string, err error) {
	ctx, cancel := context.WithTimeout(parent, dnscache.ServerResolveTimeout)
	results := make(chan relayDNSResult, 2)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
	}()
	start := func(network string) {
		workers.Go(func() {
			wire, err := exchangeRelayDNS(ctx, dial, network, destination, query, payload, limit)
			results <- relayDNSResult{wire: wire, network: network, err: err}
		})
	}

	start("udp")
	timer := time.NewTimer(dnsTCPFallbackDelay)
	defer timer.Stop()
	pending := 2
	var exchangeErr error
	select {
	case result := <-results:
		if result.err == nil {
			return result.wire, result.network, nil
		}
		exchangeErr = result.err
		pending--
	case <-timer.C:
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	start(networkTCP)
	for range pending {
		select {
		case result := <-results:
			if result.err == nil {
				return result.wire, result.network, nil
			}
			exchangeErr = errors.Join(exchangeErr, result.err)
		case <-ctx.Done():
			return nil, "", errors.Join(exchangeErr, ctx.Err())
		}
	}
	return nil, "", exchangeErr
}

func exchangeRelayDNS(
	ctx context.Context, dial relayDNSDialer, network, destination string, query *dns.Msg, payload []byte, limit int,
) ([]byte, error) {
	conn, err := dial(ctx, network, destination)
	if err != nil {
		return nil, fmt.Errorf("%s DNS dial: %w", network, err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer func() {
		stop()
		_ = conn.Close()
	}()

	wire := conn
	expected := len(payload)
	if network == networkTCP {
		wire = &dns.Conn{Conn: conn}
		expected += 2 // DNS-over-TCP length prefix.
	}
	n, err := wire.Write(payload)
	if err != nil {
		return nil, fmt.Errorf("%s DNS write: %w", network, err)
	}
	if n != expected {
		return nil, fmt.Errorf("%s DNS write: %w", network, io.ErrShortWrite)
	}
	buffer := make([]byte, dns.MaxMsgSize)
	n, err = wire.Read(buffer)
	if err != nil {
		return nil, fmt.Errorf("%s DNS read: %w", network, err)
	}
	raw := buffer[:n]
	var response dns.Msg
	if err := response.Unpack(raw); err != nil {
		return nil, fmt.Errorf("%s DNS response: %w", network, err)
	}
	if !response.Response || response.Id != query.Id || response.Opcode != query.Opcode || len(response.Question) != 1 ||
		!strings.EqualFold(response.Question[0].Name, query.Question[0].Name) ||
		response.Question[0].Qtype != query.Question[0].Qtype || response.Question[0].Qclass != query.Question[0].Qclass {
		return nil, fmt.Errorf("%s DNS response does not match the query", network)
	}
	if network == networkTCP && len(raw) > limit {
		response.Truncate(limit)
		return response.Pack()
	}
	return raw, nil
}
