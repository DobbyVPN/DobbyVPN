package sessionapi

import (
	"context"
	"errors"

	"core/log"
)

// A FAILED session keeps its network so the kill switch stays on, and a Retry
// normally reopens only the protocol on it. After a network-level failure the
// network itself may be stale, for example after the system woke from sleep,
// so a Retry then rebuilds it. Runtimes whose protection can outlive their
// network implement NetworkRebuilder; others keep the network as before.

// NetworkRebuilder is implemented by a NetworkRuntime whose network can be
// released with its protection kept applied: the release context carries
// WithKeptProtection, and the next network adopts the protection.
type NetworkRebuilder interface {
	// ReleaseKeptProtection removes protection that a released network kept
	// when no new network adopted it.
	ReleaseKeptProtection(context.Context) error
}

type keptProtectionKey struct{}

// WithKeptProtection marks a network release whose protection must stay
// applied for the next network.
func WithKeptProtection(ctx context.Context) context.Context {
	return context.WithValue(ctx, keptProtectionKey{}, true)
}

// KeepsProtection reports a release marked by WithKeptProtection.
func KeepsProtection(ctx context.Context) bool {
	kept, _ := ctx.Value(keptProtectionKey{}).(bool)
	return kept
}

// networkLevelFailure reports failures where the network binding itself may
// be stale: no profile became ready, or the runtime or its health checks
// failed. Other codes cannot be fixed by a new binding.
func networkLevelFailure(code FailureCode) bool {
	return code == FailureRuntime || code == FailureProbe
}

// prepareRetryLocked readies a Retry from FAILED; an automatic retry uses it
// too. It asks the next attempt to rebuild the network after a network-level
// failure when the runtime keeps protection across the rebuild.
func (m *Manager) prepareRetryLocked(s *session) {
	s.recoveryCount, s.recoveryOriginGeneration = 0, 0
	_, rebuilds := m.runtime.(NetworkRebuilder)
	s.rebuildNetwork = rebuilds && s.vpnActive && networkLevelFailure(s.lastFailure)
	if !s.vpnActive {
		// Only kept protection is left after a failed rebuild; the attempt
		// acquires a new network that adopts it.
		s.networkGeneration++
		s.networkLedger = &ledger{}
	}
}

// releaseNetworkForRebuild releases the kept network before the attempt
// acquires a new one. Its protection stays applied until the new network
// adopts it.
func (m *Manager) releaseNetworkForRebuild(s *session) error {
	s.mu.Lock()
	rebuild := s.rebuildNetwork && s.vpnActive && !s.stopRequested && !s.closing
	s.rebuildNetwork = false
	work := s.networkLedger
	s.mu.Unlock()
	if !rebuild {
		return nil
	}
	err := work.release(WithKeptProtection(m.cleanupContext(s)))
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		return wrapFailure(FailureCleanup, err)
	}
	s.networkGeneration++
	s.networkLedger, s.vpnActive, s.protectionKept = &ledger{}, false, true
	log.Info("SESSION", "network released for rebuild; protection kept", map[string]any{"network_generation": s.networkGeneration})
	m.appendLocked(s)
	return nil
}

// releaseKeptProtectionLocked removes protection no network adopted, when the
// user stops or the service shuts down.
func (m *Manager) releaseKeptProtectionLocked(ctx context.Context, s *session) error {
	if !s.protectionKept {
		return nil
	}
	rebuilder, ok := m.runtime.(NetworkRebuilder)
	if !ok {
		return errors.New("kept protection has no owner to release it")
	}
	if err := rebuilder.ReleaseKeptProtection(ctx); err != nil {
		return err
	}
	s.protectionKept = false
	return nil
}
