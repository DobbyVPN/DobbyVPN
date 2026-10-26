package dnscache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"core/log"

	"github.com/miekg/dns"
	"golang.org/x/net/idna"
)

// resolveCapturedDNSServers uses only the physical network's captured server
// inventory. It does not consult global DNS configuration or add a TTL cache.
// Both record families share the caller's deadline. Racing configured servers
// leaves failover inside that budget without an earlier per-server cutoff.
func resolveCapturedDNSServers(ctx context.Context, host string, servers []string, dialer *net.Dialer, network string) ([]net.IPAddr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(servers) == 0 {
		return nil, errors.New("captured physical DNS server inventory is empty")
	}
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint DNS name %q: %w", host, err)
	}
	type result struct {
		addresses []net.IPAddr
		err       error
	}
	families := make(chan result, 2)
	for _, kind := range []uint16{dns.TypeA, dns.TypeAAAA} {
		go func(kind uint16) {
			familyContext, cancel := context.WithCancel(ctx)
			var queries sync.WaitGroup
			defer func() { cancel(); queries.Wait() }()
			replies := make(chan result, len(servers))
			for _, server := range servers {
				queries.Add(1)
				go func(server string) {
					defer queries.Done()
					started := time.Now()
					addresses, queryErr := queryCapturedDNSServer(familyContext, ascii, kind, server, dialer)
					log.Debugf(Category, "captured DNS network=%s server=%s host=%s type=%d addresses=%v elapsed=%s err=%v", network, server, host, kind, addresses, time.Since(started), queryErr)
					if queryErr != nil {
						queryErr = fmt.Errorf("physical DNS %s type=%d host=%s: %w", server, kind, host, queryErr)
					}
					replies <- result{addresses, queryErr}
				}(server)
			}
			var failures []error
			for range servers {
				reply := <-replies
				var dnsError *net.DNSError
				if reply.err == nil || errors.As(reply.err, &dnsError) && dnsError.IsNotFound {
					cancel()
					queries.Wait()
					families <- reply
					return
				}
				failures = append(failures, reply.err)
			}
			families <- result{err: errors.Join(failures...)}
		}(kind)
	}
	var addresses []net.IPAddr
	var failures []error
	for range 2 {
		family := <-families
		addresses = append(addresses, family.addresses...)
		failures = append(failures, family.err)
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ctx.Err(), errors.Join(failures...))
	}
	if len(addresses) > 0 {
		return addresses, nil
	}
	return nil, errors.Join(failures...)
}

func queryCapturedDNSServer(ctx context.Context, host string, kind uint16, server string, dialer *net.Dialer) ([]net.IPAddr, error) {
	name := strings.ToLower(dns.Fqdn(host))
	seen := make(map[string]bool)
	for {
		if seen[name] {
			return nil, fmt.Errorf("DNS alias loop at %s", name)
		}
		seen[name] = true
		question := new(dns.Msg)
		question.SetQuestion(name, kind)
		response, err := exchangeCapturedDNS(ctx, question, server, "udp", dialer)
		if err != nil {
			return nil, err
		}
		if response.Truncated {
			response, err = exchangeCapturedDNS(ctx, question, server, "tcp", dialer)
			if err != nil {
				return nil, err
			}
		}
		if response.Rcode == dns.RcodeNameError {
			return nil, &net.DNSError{Name: host, Server: server, Err: "no such host", IsNotFound: true}
		}
		if response.Rcode != dns.RcodeSuccess {
			return nil, fmt.Errorf("DNS response code %d", response.Rcode)
		}
		for {
			addresses, alias := capturedDNSAnswerRecords(response, name, kind)
			if len(addresses) > 0 || alias == "" {
				return addresses, nil
			}
			if seen[alias] {
				return nil, fmt.Errorf("DNS alias loop at %s", alias)
			}
			name = alias
			// A recursive response normally contains the target records. If it
			// does not, follow the alias through the same captured resolver.
			present := false
			for _, record := range response.Answer {
				if strings.EqualFold(record.Header().Name, name) {
					present = true
					break
				}
			}
			if !present {
				break
			}
			seen[name] = true
		}
	}
}

func capturedDNSAnswerRecords(response *dns.Msg, name string, kind uint16) (addresses []net.IPAddr, alias string) {
	for _, record := range response.Answer {
		if !strings.EqualFold(record.Header().Name, name) {
			continue
		}
		switch record := record.(type) {
		case *dns.A:
			if kind == dns.TypeA {
				addresses = append(addresses, net.IPAddr{IP: append(net.IP(nil), record.A...)})
			}
		case *dns.AAAA:
			if kind == dns.TypeAAAA {
				addresses = append(addresses, net.IPAddr{IP: append(net.IP(nil), record.AAAA...)})
			}
		case *dns.CNAME:
			alias = strings.ToLower(dns.Fqdn(record.Target))
		}
	}
	return addresses, alias
}

func exchangeCapturedDNS(ctx context.Context, question *dns.Msg, server, protocol string, dialer *net.Dialer) (_ *dns.Msg, resultErr error) {
	host, _, err := net.SplitHostPort(server)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return nil, fmt.Errorf("captured DNS server must be a numeric address: %s", server)
	}
	if ip.Unmap().Is4() {
		protocol += "4"
	} else {
		protocol += "6"
	}
	client := dns.Client{Net: protocol, Dialer: dialer, Timeout: ServerResolveTimeout}
	connection, err := client.DialContext(ctx, server)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer func() {
		stop()
		if closeErr := connection.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	response, _, err := client.ExchangeWithConnContext(ctx, question, connection)
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return response, err
}
