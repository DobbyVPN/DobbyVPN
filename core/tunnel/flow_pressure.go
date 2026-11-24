package tunnel

import (
	"io"
	"runtime"
	"time"

	"core/log"
	"core/tunnel/platform_engine"
)

type FlowPressure uint8

const (
	FlowPressureNormal FlowPressure = iota
	FlowPressureWarning
	FlowPressureCritical
)

const iosPlatform = "ios"
const iosUDPAssociationLimit = 64
const pressureIdlePeriod = 5 * time.Second
const networkGenerationField = "network_generation"

var flowClock = time.Now()

// SetFlowPressure only changes the stack owned by this provider network.
// Outstanding connections keep their existing owner across the transition.
func SetFlowPressure(session string, generation uint64, pressure FlowPressure) bool {
	if runtime.GOOS != iosPlatform || pressure > FlowPressureCritical {
		return false
	}
	engineMu.Lock()
	owner := activeEngine
	engineMu.Unlock()
	if owner == nil {
		return false
	}
	return owner.setFlowPressure(session, generation, pressure)
}

func (e *Engine) setFlowPressure(session string, generation uint64, pressure FlowPressure) bool {
	// The provider's resource queue must not wait for teardown while iOS
	// settings callbacks may be waiting to update that same queue.
	if !e.mu.TryLock() {
		return false
	}
	if !e.ownsProviderNetwork(session, generation) {
		e.mu.Unlock()
		return false
	}
	previous := e.pressure
	e.pressure = pressure
	platform_engine.SetTCPAdmissionPaused(pressure != FlowPressureNormal)
	e.router.mu.Lock()
	e.router.udpPaused = pressure != FlowPressureNormal
	e.router.mu.Unlock()
	target := e.proxy
	e.mu.Unlock()
	if previous != pressure {
		log.Info(Category, "flow pressure updated", map[string]any{
			networkGenerationField: generation, "pressure": pressure,
		})
	}
	if pressure == FlowPressureCritical && target != nil && target.reclaiming.CompareAndSwap(false, true) {
		go func() {
			defer target.reclaiming.Store(false)
			if reclaimed := target.flows.closeMatching(time.Now().Add(-pressureIdlePeriod)); reclaimed > 0 {
				log.Info(Category, "idle flows reclaimed", map[string]any{
					networkGenerationField: generation, "generation": target.generation, "reclaimed_flows": reclaimed,
				})
			}
		}()
	}
	return true
}

func ActiveFlowGeneration() uint64 {
	if target := activeFlowDiagnostics.Load(); target != nil {
		return target.generation
	}
	return 0
}

// CloseFlowsForPath drops flows owned by the attempt active when the physical
// path changed. An older notification cannot close flows of its replacement.
func CloseFlowsForPath(session string, generation, attempt, revision uint64) bool {
	if runtime.GOOS != iosPlatform {
		return false
	}
	engineMu.Lock()
	owner := activeEngine
	engineMu.Unlock()
	if owner == nil {
		return false
	}
	return owner.closeFlowsForPath(session, generation, attempt, revision)
}

func (e *Engine) closeFlowsForPath(session string, generation, attempt, revision uint64) bool {
	e.mu.Lock()
	if !e.ownsProviderNetwork(session, generation) || e.proxy == nil || e.attempt != attempt || revision <= e.physicalRevision {
		e.mu.Unlock()
		return false
	}
	target := e.proxy
	e.physicalRevision = revision
	platform_engine.CancelPendingTCPAdmissions()
	target.flows.cancelPendingTCP()
	connections := target.flows.connectionSnapshot()
	e.mu.Unlock()
	closed := 0
	for _, conn := range connections {
		if err := conn.Close(); err == nil {
			closed++
		}
	}
	log.Info(Category, "physical path flows invalidated", map[string]any{
		networkGenerationField: generation, "generation": attempt, "path_revision": revision, "closed_flows": closed,
	})
	return true
}

func (e *Engine) ownsProviderNetwork(session string, generation uint64) bool {
	return !e.stopped && e.router != nil && session != "" && generation != 0 &&
		e.networkSession == session && e.networkGeneration == generation
}

// Connection wrappers can unregister themselves on Close. Take the snapshot
// before acquiring any wrapper lock.
func (o *flowOwner) connectionSnapshot() []io.Closer {
	o.mu.Lock()
	connections := make([]io.Closer, 0, len(o.connections))
	for conn := range o.connections {
		connections = append(connections, conn)
	}
	o.mu.Unlock()
	return connections
}

func (o *flowOwner) closeMatching(cutoff time.Time) int {
	closed := 0
	for _, conn := range o.connectionSnapshot() {
		if flow, ok := conn.(interface{ closeIfIdle(time.Time) bool }); ok && flow.closeIfIdle(cutoff) {
			closed++
		}
	}
	return closed
}

func lastFlowActivity(start time.Time, observation int64) time.Time {
	if observation == 0 {
		return start
	}
	return flowClock.Add(time.Duration(observation))
}

func (c *trackedConn) closeIfIdle(cutoff time.Time) bool {
	return lastFlowActivity(c.started, c.lastIO.Load()).Before(cutoff) && c.Close() == nil
}

func (c *idlePacketConn) closeIfIdle(cutoff time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed && c.lastTouch.Before(cutoff) && c.closeLocked() == nil
}

func (c *trackedUDPOrigin) closeIfIdle(cutoff time.Time) bool {
	return lastFlowActivity(c.started, c.lastIO.Load()).Before(cutoff) && c.Close() == nil
}
