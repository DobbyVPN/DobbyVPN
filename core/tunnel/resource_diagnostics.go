package tunnel

import (
	"fmt"
	"runtime/metrics"
)

// ResourceDiagnostics samples the current attempt without waiting for lifecycle
// locks. It is safe to call from a platform state notification during teardown.
func ResourceDiagnostics() string {
	samples := []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(samples)
	heap := samples[0].Value.Uint64()
	total := samples[1].Value.Uint64()
	released := samples[2].Value.Uint64()
	var activeTCP, peakTCP, activeUDP, peakUDP int64
	var generation uint64
	if proxy := activeFlowDiagnostics.Load(); proxy != nil {
		generation = proxy.generation
		activeTCP, peakTCP = proxy.activeTCP.Load(), proxy.peakTCP.Load()
		activeUDP, peakUDP = proxy.activeUDP.Load(), proxy.peakUDP.Load()
	}
	return fmt.Sprintf("go_heap_bytes=%d go_runtime_bytes=%d generation=%d activeTCP=%d peakTCP=%d activeUDP=%d peakUDP=%d",
		heap, total-released, generation, activeTCP, peakTCP, activeUDP, peakUDP)
}
