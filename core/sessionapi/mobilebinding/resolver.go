package mobilebinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"core/dnscache"
	"core/sessionapi"
)

type networkContextCallbacks interface {
	UnderlyingNetwork(sessionID string, generation int64) string
}

// Android uses DnsResolver on the captured Network. Apple supplies a physical
// interface index for the native DNSService resolver in dnscache.
type networkLookupCallbacks interface {
	ResolveUnderlying(sessionID string, generation int64, revision string, queryID int64, host string, timeoutMillis int64) string
	CancelUnderlying(queryID int64)
	FinishUnderlying(queryID int64)
}

type underlyingNetwork struct {
	Revision       string `json:"revision"`
	InterfaceIndex int    `json:"interface_index"`
	Error          string `json:"error"`
}

var nextDNSQuery atomic.Int64

func (p *platformAdapter) captureUnderlyingNetwork(ctx context.Context, ref sessionapi.SessionRef, generation int64, native networkContextCallbacks) (underlyingNetwork, error) {
	var network underlyingNetwork
	if err := ctx.Err(); err != nil {
		return network, err
	}
	p.mu.Lock()
	active := p.active[ref.SessionID] == ref
	p.mu.Unlock()
	if !active {
		return network, errors.New("underlying network owner is obsolete")
	}
	if err := json.Unmarshal([]byte(native.UnderlyingNetwork(ref.SessionID, generation)), &network); err != nil {
		return network, err
	}
	if network.Error != "" {
		return network, errors.New(network.Error)
	}
	if network.Revision == "" {
		return network, errors.New("underlying network has no revision")
	}
	return network, nil
}

func newNativeUnderlyingResolver(lookup networkLookupCallbacks, ref sessionapi.SessionRef, generation int64, revision string) dnscache.Resolver {
	return func(ctx context.Context, host string) ([]net.IPAddr, error) {
		id := nextDNSQuery.Add(1)
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, errors.New("native DNS query requires a deadline")
		}
		remaining := time.Until(deadline).Milliseconds()
		if remaining <= 0 {
			return nil, context.DeadlineExceeded
		}
		cancelDone := make(chan struct{})
		cancel := context.AfterFunc(ctx, func() { lookup.CancelUnderlying(id); close(cancelDone) })
		raw := lookup.ResolveUnderlying(ref.SessionID, generation, revision, id, host, remaining)
		if !cancel() {
			<-cancelDone
		}
		lookup.FinishUnderlying(id)
		var result struct {
			Addresses []string `json:"addresses"`
			Error     string   `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			return nil, err
		}
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		addresses := make([]net.IPAddr, 0, len(result.Addresses))
		for _, value := range result.Addresses {
			address, err := netip.ParseAddr(value)
			if err != nil {
				return nil, fmt.Errorf("native DNS address %q: %w", value, err)
			}
			addresses = append(addresses, net.IPAddr{IP: net.IP(address.AsSlice()), Zone: address.Zone()})
		}
		return addresses, nil
	}
}

func (p *platformAdapter) ConfigureResolver(ctx context.Context, ref sessionapi.SessionRef, cache *dnscache.Cache) error {
	p.mu.Lock()
	callbacks := p.callbacks
	p.mu.Unlock()
	native, ok := callbacks.(networkContextCallbacks)
	if !ok {
		return errors.New("native underlying network context is unavailable")
	}
	generation, err := generationAsInt64(ref.Generation)
	if err != nil {
		return err
	}
	if _, err := p.captureUnderlyingNetwork(ctx, ref, generation, native); err != nil {
		return err
	}
	cache.SetResolverSource(func(ctx context.Context) (string, dnscache.Resolver, error) {
		network, err := p.captureUnderlyingNetwork(ctx, ref, generation, native)
		if err != nil {
			return "", nil, err
		}
		var resolve dnscache.Resolver
		if lookup, ok := callbacks.(networkLookupCallbacks); ok {
			resolve = newNativeUnderlyingResolver(lookup, ref, generation, network.Revision)
		} else {
			scoped := dnscache.New()
			if err := scoped.UseInterface(network.InterfaceIndex); err != nil {
				return "", nil, err
			}
			resolve = scoped.ResolveAddresses
		}
		return network.Revision, func(ctx context.Context, host string) ([]net.IPAddr, error) {
			addresses, err := resolve(ctx, host)
			current, captureErr := p.captureUnderlyingNetwork(ctx, ref, generation, native)
			if captureErr != nil {
				return nil, errors.Join(err, captureErr)
			}
			if current != network {
				return nil, errors.Join(err, errors.New("native underlying network changed during DNS query"))
			}
			return addresses, err
		}, nil
	})
	return nil
}
