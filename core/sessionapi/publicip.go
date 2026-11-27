package sessionapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"core/log"
	"core/probe"
	"core/publicip"
)

const (
	// publicIPBeforeTimeout bounds the pre-tunnel lookup. The attempt waits for
	// it before the tunnel takes over routing, so a late answer can never come
	// from the VPN egress and be shown as the "before" address.
	publicIPBeforeTimeout = 3 * time.Second
	// publicIPAfterTimeout bounds the through-tunnel lookup.
	publicIPAfterTimeout = 10 * time.Second
)

// publicIPLookup fetches the public address through the vantage point of the
// supplied client. It exists so tests can replace the network lookup.
type publicIPLookup func(ctx context.Context, client *http.Client, endpoints []string) (string, error)

func (m *Manager) publicIPLookupFunc() publicIPLookup {
	if m.lookupPublicIP != nil {
		return m.lookupPublicIP
	}
	return publicip.Lookup
}

// measurePublicIPBefore records the attempt's pre-tunnel observation. runStart
// calls it before prepareNetwork, so a connection started from idle observes
// the direct egress; a switch keeps the network lease and observes the egress
// still active at that moment. It runs synchronously and is skipped when the
// attempt's record already carries a settled before value (health-recovery
// continuations and automatic retries keep it).
func (m *Manager) measurePublicIPBefore(ctx context.Context, s *session) {
	s.mu.Lock()
	rec := s.publicIP
	needed := rec != nil && rec.Before.State == PublicIPMeasuring
	s.mu.Unlock()
	if !needed {
		return
	}
	measureCtx, cancel := context.WithTimeout(ctx, publicIPBeforeTimeout)
	defer cancel()
	value, err := m.publicIPLookupFunc()(measureCtx, &http.Client{Timeout: publicIPBeforeTimeout}, publicip.DefaultEndpoints)
	m.settlePublicIP(s, rec, "before", value, err)
}

// startPublicIPAfter publishes the after observation of the connected attempt
// and queries it through the tunnel's local SOCKS endpoint, so the value is the
// VPN egress on every platform. A direct socket cannot be used: on iOS the
// packet-tunnel provider's own sockets bypass its tunnel.
func (m *Manager) startPublicIPAfter(ctx context.Context, s *session, lease RuntimeLease) {
	s.mu.Lock()
	rec := s.publicIP
	if rec != nil {
		rec.After = PublicIPMeasurement{State: PublicIPMeasuring}
		m.appendLocked(s)
	}
	s.mu.Unlock()
	if rec == nil {
		return
	}
	proxied, ok := lease.(ProxyLease)
	if !ok || proxied.ProxyAddr() == "" {
		m.settlePublicIP(s, rec, "after", "", errors.New("the connected lease exposes no SOCKS endpoint"))
		return
	}
	go m.measurePublicIPAfter(ctx, s, rec, proxied.ProxyAddr())
}

func (m *Manager) measurePublicIPAfter(ctx context.Context, s *session, rec *PublicIP, proxyAddr string) {
	measureCtx, cancel := context.WithTimeout(ctx, publicIPAfterTimeout)
	defer cancel()
	dial, err := probe.NewSOCKS5DialContext(proxyAddr, publicIPAfterTimeout)
	if err != nil {
		m.settlePublicIP(s, rec, "after", "", err)
		return
	}
	client := &http.Client{
		Timeout:   publicIPAfterTimeout,
		Transport: &http.Transport{DialContext: dial, DisableKeepAlives: true},
	}
	value, err := m.publicIPLookupFunc()(measureCtx, client, publicip.DefaultEndpoints)
	m.settlePublicIP(s, rec, "after", value, err)
}

// settlePublicIP records one observation outcome on the record it was started
// for. A record replaced by a later attempt rejects the write, so a slow lookup
// can never present as the current connection's value. Outcomes are logged;
// the address itself never is (diagnostics exports must stay free of user data).
func (m *Manager) settlePublicIP(s *session, rec *PublicIP, phase, value string, err error) {
	state := PublicIPObserved
	if err != nil {
		state, value = PublicIPUnavailable, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec == nil || s.publicIP != rec {
		return
	}
	target := &rec.Before
	if phase == "after" {
		target = &rec.After
	}
	*target = PublicIPMeasurement{State: state, Value: value}
	log.Info("SESSION", "public IP observation", map[string]any{
		"session_id": s.id, logGeneration: s.generation, "phase": phase, "outcome": state,
	})
	m.appendLocked(s)
}

// snapshotPublicIP returns a copy so a later observation write cannot mutate a
// snapshot a caller already holds.
func snapshotPublicIP(s *session) *PublicIP {
	if s.publicIP == nil {
		return nil
	}
	publicIP := *s.publicIP
	return &publicIP
}
